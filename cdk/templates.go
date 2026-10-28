package main

// PortRange is an inclusive port range a game needs reachable from the internet.
type PortRange struct {
	From float64
	To   float64
}

// GameTemplate is what every server of one game shares.
type GameTemplate struct {
	// Game identifies the template, and is the value of a server's game-name tag.
	Game string
	// DisplayName is what the control panel shows. Several servers of one game share it.
	DisplayName string

	TcpPorts []PortRange
	UdpPorts []PortRange

	// InstallScript is the path under the published scripts base URL. The script fetches its own
	// companion from alongside itself, so both have to stay side by side wherever they are hosted.
	InstallScript string
	// CompanionScript reports the server's status; ApplyConfigScript brings its config into line with
	// the control panel before the game starts. Both sit beside the install script.
	CompanionScript   string
	ApplyConfigScript string

	// DataPath is where the server's data volume mounts. WorldDownloadPaths and WorldDownloadCorePaths
	// are the subfolders of it that the two download actions include.
	DataPath               string
	WorldDownloadPaths     string
	WorldDownloadCorePaths string

	DefaultInstanceType  string
	DefaultVolumeSizeGib float64

	// RequiresGameVersion is set for a game whose install script needs an exact version.
	RequiresGameVersion bool
}

var GameTemplates = []GameTemplate{
	{
		Game:        "valheim",
		DisplayName: "Valheim",
		UdpPorts:    []PortRange{{From: 2456, To: 2458}},

		InstallScript:     "Valheim/install.sh",
		CompanionScript:   "Valheim/status_agent.py",
		ApplyConfigScript: "Valheim/apply_config.sh",

		DataPath:               "/usr/games/serverconfig/valheim",
		WorldDownloadPaths:     "saves,server/BepInEx",
		WorldDownloadCorePaths: "saves",

		DefaultInstanceType:  "t3a.medium",
		DefaultVolumeSizeGib: 10,
	},
	{
		Game:        "vintagestory",
		DisplayName: "Vintage Story",
		TcpPorts:    []PortRange{{From: 42420, To: 42420}},
		UdpPorts:    []PortRange{{From: 42420, To: 42420}},

		InstallScript:     "VintageStory/install.sh",
		CompanionScript:   "VintageStory/status_agent.py",
		ApplyConfigScript: "VintageStory/apply_config.sh",

		DataPath:               "/home/vintagestory/data",
		WorldDownloadPaths:     "Saves,Mods,Playerdata",
		WorldDownloadCorePaths: "Saves",

		DefaultInstanceType:  "t3a.medium",
		DefaultVolumeSizeGib: 10,

		RequiresGameVersion: true,
	},
}
