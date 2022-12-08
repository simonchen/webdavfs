module github.com/miquels/webdavfs

go 1.12

require (
	github.com/simonchen/fuse v0.0.0-20221208023804-aedac8a1e8ad
	github.com/pborman/getopt/v2 v2.1.0
	golang.org/x/net v0.1.0
)

replace github.com/simonchen/fuse => github.com/simonchen/fuse v0.0.0-20221208023804-aedac8a1e8ad // pin to latest version that supports macOS. see https://github.com/bazil/fuse/issues/224
