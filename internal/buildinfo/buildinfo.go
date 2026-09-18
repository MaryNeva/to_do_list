package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var (
	version = ""
	commit  = ""
	date    = ""
)

const unknown = "unknown"

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"built_at"`
	GoVersion string `json:"go_version"`
}

func Read() Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		BuiltAt:   date,
		GoVersion: runtime.Version(),
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = setting.Value
				}
			case "vcs.time":
				if info.BuiltAt == "" {
					info.BuiltAt = setting.Value
				}
			case "vcs.modified":
				if setting.Value == "true" && info.Version == "" {
					info.Version = "dev-dirty"
				}
			}
		}
	}

	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = unknown
	}
	if info.BuiltAt == "" {
		info.BuiltAt = unknown
	}

	return info
}
