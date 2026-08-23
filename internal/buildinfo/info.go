package buildinfo

import "runtime"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
	Platform  string
}

func Current() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}
