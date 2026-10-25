package buildinfo

import "runtime/debug"

var Version = "dev"
var Commit = "unknown"
var Date = "unknown"

const ModulePath = "github.com/rubyu/artifact-registry-go"

func LibraryVersion() string {
	if Version != "dev" {
		return Version
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		if build.Main.Path == ModulePath && build.Main.Version != "" && build.Main.Version != "(devel)" {
			return build.Main.Version
		}
		for _, dependency := range build.Deps {
			if dependency.Path == ModulePath {
				if dependency.Replace != nil {
					dependency = dependency.Replace
				}
				if dependency.Version != "" && dependency.Version != "(devel)" {
					return dependency.Version
				}
			}
		}
	}
	return "dev"
}

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func Current() Info {
	info := Info{Version, Commit, Date}
	if build, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
			info.Version = build.Main.Version
		}
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" && info.Commit == "unknown" {
				info.Commit = setting.Value
			}
			if setting.Key == "vcs.time" && info.Date == "unknown" {
				info.Date = setting.Value
			}
		}
	}
	return info
}
