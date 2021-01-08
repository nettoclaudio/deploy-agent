module github.com/tsuru/deploy-agent

go 1.12

require (
	github.com/AkihiroSuda/nerdctl v0.4.1-0.20210104033751-ac9758b9eab3
	github.com/containerd/containerd v1.4.1
	github.com/fsouza/go-dockerclient v1.7.0
	github.com/ghodss/yaml v1.0.0
	github.com/kelseyhightower/envconfig v1.3.0
	github.com/tsuru/commandmocker v0.0.0-20160909010208-e1d28f4f616a // indirect
	github.com/tsuru/tsuru v0.0.0-20171023121507-c91725578089
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f
)

replace (
	github.com/containerd/stargz-snapshotter/estargz => github.com/containerd/stargz-snapshotter/estargz v0.0.0-20210101143201-d58f43a8235e
	github.com/fsouza/go-dockerclient => github.com/cezarsa/go-dockerclient v0.0.0-20210107161031-535fe726dda5
)
