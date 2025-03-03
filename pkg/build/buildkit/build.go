// Copyright 2023 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package buildkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alessio/shellescape"
	"github.com/containerd/console"
	"github.com/docker/cli/cli/config"
	containerregistryauthn "github.com/google/go-containerregistry/pkg/authn"
	containerregistryname "github.com/google/go-containerregistry/pkg/name"
	containerregistrygoogle "github.com/google/go-containerregistry/pkg/v1/google"
	containerregistryremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/moby/buildkit/client"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/progress/progresswriter"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	"github.com/tsuru/deploy-agent/pkg/build"
	pb "github.com/tsuru/deploy-agent/pkg/build/grpc_build_v1"
	"github.com/tsuru/deploy-agent/pkg/util"
)

var _ build.Builder = (*BuildKit)(nil)

type BuildKitOptions struct {
	TempDir string
}

type BuildKit struct {
	cli  *client.Client
	opts BuildKitOptions
}

func NewBuildKit(c *client.Client, opts BuildKitOptions) *BuildKit {
	return &BuildKit{cli: c, opts: opts}
}

func (b *BuildKit) Build(ctx context.Context, r *pb.BuildRequest, w io.Writer) (*pb.TsuruConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ow, ok := w.(console.File)
	if !ok {
		return nil, errors.New("writer must implement console.File")
	}

	switch pb.BuildKind_name[int32(r.Kind)] {
	case "BUILD_KIND_APP_BUILD_WITH_SOURCE_UPLOAD":
		return b.buildFromAppSourceFiles(ctx, r, ow)

	case "BUILD_KIND_APP_BUILD_WITH_CONTAINER_IMAGE":
		return b.buildFromContainerImage(ctx, r, ow)

	case "BUILD_KIND_APP_BUILD_WITH_CONTAINER_FILE":
		return b.buildFromContainerFile(ctx, r, ow)

	case "BUILD_KIND_PLATFORM_WITH_CONTAINER_FILE":
		return nil, b.buildPlatform(ctx, r, ow)
	}

	return nil, status.Errorf(codes.Unimplemented, "build kind not supported")
}

func (b *BuildKit) buildFromAppSourceFiles(ctx context.Context, r *pb.BuildRequest, w console.File) (*pb.TsuruConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	appFiles, err := build.ExtractTsuruAppFilesFromAppSourceContext(ctx, bytes.NewBuffer(r.Data))
	if err != nil {
		return nil, err
	}

	var dockerfile bytes.Buffer
	if err = generateContainerfile(&dockerfile, r.SourceImage, appFiles); err != nil {
		return nil, err
	}

	var envs map[string]string
	if r.App != nil {
		envs = r.App.EnvVars
	}

	tmpDir, cleanFunc, err := generateBuildLocalDir(ctx, b.opts.TempDir, dockerfile.String(), bytes.NewBuffer(r.Data), envs, nil)
	if err != nil {
		return nil, err
	}
	defer cleanFunc()

	if err = b.callBuildKitBuild(ctx, tmpDir, r, w); err != nil {
		return nil, err
	}

	// NOTE(nettoclaudio): Some platforms don't require an user-defined Procfile (e.g. go, java, static, etc).
	// So we need to retrieve the default Procfile from the platform image.
	if appFiles.Procfile == "" {
		fmt.Fprintln(w, "User-defined Procfile not found, trying to extract it from platform's container image")

		tc, err := b.extractTsuruConfigsFromContainerImage(ctx, r.DestinationImages[0], build.DefaultTsuruPlatformWorkingDir)
		if err != nil {
			return nil, err
		}

		appFiles.Procfile = tc.Procfile
	}

	return appFiles, nil
}

func generateContainerfile(w io.Writer, image string, tsuruAppFiles *pb.TsuruConfig) error {
	var tsuruYaml build.TsuruYamlData
	if tsuruAppFiles != nil {
		if err := yaml.Unmarshal([]byte(tsuruAppFiles.TsuruYaml), &tsuruYaml); err != nil {
			return err
		}
	}

	var buildHooks []string
	if hooks := tsuruYaml.Hooks; hooks != nil {
		buildHooks = hooks.Build
	}

	dockerfile, err := build.BuildContainerfile(build.BuildContainerfileParams{
		Image:      image,
		BuildHooks: buildHooks,
	})
	if err != nil {
		return err
	}

	_, err = io.WriteString(w, dockerfile)
	return err
}

func (b *BuildKit) buildFromContainerImage(ctx context.Context, r *pb.BuildRequest, w console.File) (*pb.TsuruConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tmpDir, cleanFunc, err := generateBuildLocalDir(ctx, b.opts.TempDir, fmt.Sprintf("FROM %s", r.SourceImage), nil, nil, nil)
	if err != nil {
		return nil, err
	}
	defer cleanFunc()

	if err = b.callBuildKitBuild(ctx, tmpDir, r, w); err != nil {
		return nil, err
	}

	var insecureRegistry bool
	if r.PushOptions != nil {
		insecureRegistry = r.PushOptions.InsecureRegistry
	}

	imageConfig, err := extractContainerImageConfigFromImageManifest(ctx, r.DestinationImages[0], insecureRegistry)
	if err != nil {
		return nil, err
	}

	appFiles, err := b.callBuildKitToExtractTsuruConfigs(ctx, tmpDir, imageConfig.WorkingDir)
	if err != nil {
		return nil, err
	}

	appFiles.ImageConfig = imageConfig
	return appFiles, nil
}

func (b *BuildKit) extractTsuruConfigsFromContainerImage(ctx context.Context, image, workingDir string) (*pb.TsuruConfig, error) {
	tmpDir, cleanFunc, err := generateBuildLocalDir(ctx, b.opts.TempDir, fmt.Sprintf("FROM %s", image), nil, nil, nil)
	if err != nil {
		return nil, err
	}
	defer cleanFunc()

	return b.callBuildKitToExtractTsuruConfigs(ctx, tmpDir, workingDir)
}

func (b *BuildKit) callBuildKitToExtractTsuruConfigs(ctx context.Context, localContextDir, workingDir string) (*pb.TsuruConfig, error) {
	eg, ctx := errgroup.WithContext(ctx)
	pr, pw := io.Pipe() // reader/writer for tar output

	eg.Go(func() error {
		opts := client.SolveOpt{
			Frontend: "dockerfile.v0",
			LocalDirs: map[string]string{
				"context":    filepath.Join(localContextDir, "context"),
				"dockerfile": localContextDir,
			},
			Exports: []client.ExportEntry{
				{
					Type: client.ExporterTar,
					Output: func(_ map[string]string) (io.WriteCloser, error) {
						return pw, nil
					},
				},
			},
			Session: []session.Attachable{
				authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr)),
			},
		}
		_, err := b.cli.Build(ctx, opts, "deploy-agent", func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
			return c.Solve(ctx, gateway.SolveRequest{
				Frontend:    opts.Frontend,
				FrontendOpt: opts.FrontendAttrs,
			})
		}, nil)
		return err
	})

	var tc *pb.TsuruConfig
	eg.Go(func() error {
		var err error
		tc, err = build.ExtractTsuruAppFilesFromContainerImageTarball(ctx, pr, workingDir)
		return err
	})

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return tc, nil
}

func extractContainerImageConfigFromImageManifest(ctx context.Context, imageStr string, insecureRegistry bool) (*pb.ContainerImageConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var nameOpts []containerregistryname.Option
	if insecureRegistry {
		nameOpts = append(nameOpts, containerregistryname.Insecure)
	}

	ref, err := containerregistryname.ParseReference(imageStr, nameOpts...)
	if err != nil {
		return nil, err
	}

	remoteOpts := []containerregistryremote.Option{
		containerregistryremote.WithContext(ctx),
		containerregistryremote.WithAuthFromKeychain(containerregistryauthn.NewMultiKeychain(containerregistryauthn.DefaultKeychain, containerregistrygoogle.Keychain)),
	}

	image, err := containerregistryremote.Image(ref, remoteOpts...)
	if err != nil {
		return nil, err
	}

	cf, err := image.ConfigFile()
	if err != nil {
		return nil, err
	}

	var exposedPorts []string
	for k := range cf.Config.ExposedPorts {
		exposedPorts = append(exposedPorts, k)
	}

	return &pb.ContainerImageConfig{
		Entrypoint:   cf.Config.Entrypoint,
		Cmd:          cf.Config.Cmd,
		WorkingDir:   cf.Config.WorkingDir,
		ExposedPorts: exposedPorts,
	}, nil
}

func generateBuildLocalDir(ctx context.Context, baseDir, dockerfile string, appArchiveData io.Reader, envs map[string]string, files io.Reader) (string, func(), error) {
	noopFunc := func() {}

	if err := ctx.Err(); err != nil {
		return "", noopFunc, err
	}

	// Layout design
	//
	// ./                       # Root dir
	//   Dockerfile
	//   secrets/
	//     envs.sh              # Tsuru app's env vars
	//   context/
	//     application.tar.gz   # Tsuru app's deploy data
	//     ...
	//     [other files]

	rootDir, err := os.MkdirTemp(baseDir, "deploy-agent-*")
	if err != nil {
		return "", noopFunc, status.Errorf(codes.Internal, "failed to create temp dir: %s", err)
	}

	contextDir := filepath.Join(rootDir, "context")
	if err = os.Mkdir(contextDir, 0755); err != nil {
		return "", noopFunc, err
	}

	secretsDir := filepath.Join(rootDir, "secrets")
	if err = os.Mkdir(secretsDir, 0700); err != nil {
		return "", noopFunc, err
	}

	eg, nctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		d, nerr := os.Create(filepath.Join(rootDir, "Dockerfile"))
		if nerr != nil {
			return status.Errorf(codes.Internal, "cannot create Dockerfile in %s: %s", rootDir, nerr)
		}
		defer d.Close()
		_, nerr = io.WriteString(d, dockerfile)
		return nerr
	})

	eg.Go(func() error {
		if appArchiveData == nil { // there's no application.tar.gz file, skipping it
			return nil
		}
		appArchive, nerr := os.Create(filepath.Join(contextDir, "application.tar.gz"))
		if nerr != nil {
			return status.Errorf(codes.Internal, "cannot create application archive: %s", nerr)
		}
		defer appArchive.Close()
		_, nerr = io.Copy(appArchive, appArchiveData)
		return nerr
	})

	eg.Go(func() error {
		envsFile, nerr := os.Create(filepath.Join(secretsDir, "envs.sh"))
		if nerr != nil {
			return nerr
		}
		defer envsFile.Close()
		fmt.Fprintln(envsFile, "# File containing the env vars of Tsuru app. Generated by deploy-agent.")
		for k, v := range envs {
			fmt.Fprintf(envsFile, "export %s=%s\n", k, shellescape.Quote(v))
		}
		return nil
	})

	eg.Go(func() error {
		if files == nil {
			return nil
		}

		return util.ExtractGZIPFileToDir(nctx, files, contextDir)
	})

	if err = eg.Wait(); err != nil {
		return "", noopFunc, err
	}

	return rootDir, func() { os.RemoveAll(rootDir) }, nil
}

func (b *BuildKit) buildFromContainerFile(ctx context.Context, r *pb.BuildRequest, w console.File) (*pb.TsuruConfig, error) {
	var files io.Reader
	if len(r.Data) > 0 {
		files = bytes.NewReader(r.Data)
	}

	tmpDir, cleanFunc, err := generateBuildLocalDir(ctx, b.opts.TempDir, r.Containerfile, nil, r.App.EnvVars, files)
	if err != nil {
		return nil, err
	}
	defer cleanFunc()

	if err = b.callBuildKitBuild(ctx, tmpDir, r, w); err != nil {
		return nil, err
	}

	var insecureRegistry bool
	if r.PushOptions != nil {
		insecureRegistry = r.PushOptions.InsecureRegistry
	}

	ic, err := extractContainerImageConfigFromImageManifest(ctx, r.DestinationImages[0], insecureRegistry)
	if err != nil {
		return nil, err
	}

	tc, err := b.extractTsuruConfigsFromContainerImage(ctx, r.DestinationImages[0], ic.WorkingDir)
	if err != nil {
		return nil, err
	}

	tc.ImageConfig = ic

	return tc, nil
}

func (b *BuildKit) buildPlatform(ctx context.Context, r *pb.BuildRequest, w console.File) error {
	tmpDir, cleanFunc, err := generateBuildLocalDir(ctx, b.opts.TempDir, r.Containerfile, nil, nil, nil)
	if err != nil {
		return err
	}
	defer cleanFunc()

	return b.callBuildKitBuild(ctx, tmpDir, r, w)
}

func (b *BuildKit) callBuildKitBuild(ctx context.Context, buildContextDir string, r *pb.BuildRequest, w console.File) error {
	var secretSources []secretsprovider.Source
	if r.App != nil {
		secretSources = append(secretSources, secretsprovider.Source{
			ID:       "tsuru-app-envvars",
			FilePath: filepath.Join(buildContextDir, "secrets", "envs.sh"),
		})
	}

	secrets, err := secretsprovider.NewStore(secretSources)
	if err != nil {
		return err
	}

	pw, err := progresswriter.NewPrinter(context.Background(), w, "plain") //nolint - using an empty context intentionally
	if err != nil {
		return err
	}

	eg, nctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		var insecureRegistry bool // disabled by default
		var pushImage bool = true // enabled by default

		if pots := r.PushOptions; pots != nil {
			pushImage = !pots.Disable
			insecureRegistry = pots.InsecureRegistry
		}

		opts := client.SolveOpt{
			Frontend: "dockerfile.v0",
			FrontendAttrs: map[string]string{
				// NOTE: we should always run the deploy's script command as user might
				// need to regenerate assets, for example.
				"build-arg:tsuru_deploy_cache": strconv.FormatInt(time.Now().Unix(), 10),
			},
			LocalDirs: map[string]string{
				"context":    filepath.Join(buildContextDir, "context"),
				"dockerfile": buildContextDir,
			},
			Exports: []client.ExportEntry{
				{
					Type: client.ExporterImage,
					Attrs: map[string]string{
						"name":              strings.Join(r.DestinationImages, ","),
						"push":              strconv.FormatBool(pushImage),
						"registry.insecure": strconv.FormatBool(insecureRegistry),
					},
				},
			},
			Session: []session.Attachable{
				authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr)),
				secretsprovider.NewSecretProvider(secrets),
			},
		}

		_, err = b.cli.Build(nctx, opts, "deploy-agent", func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
			return c.Solve(ctx, gateway.SolveRequest{
				Frontend:    opts.Frontend,
				FrontendOpt: opts.FrontendAttrs,
			})
		}, progresswriter.ResetTime(pw).Status())
		return err
	})

	eg.Go(func() error {
		<-pw.Done()
		return pw.Err()
	})

	return eg.Wait()
}
