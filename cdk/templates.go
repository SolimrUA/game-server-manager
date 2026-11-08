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

	// InstallScript is the path under the published scripts root, with its digest list published
	// beside it.
	InstallScript string
	// Scripts are the other files install.sh fetches, named relative to the published scripts root so
	// that a game can name one of its own or the shared controller. Listing one here is what gives it
	// a digest, and so what lets install.sh run it at all.
	Scripts []string

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

		InstallScript: "Valheim/install.sh",
		Scripts: []string{
			"Valheim/status_agent.py",
			"Valheim/apply_config.sh",
			"controller.sh",
			"lib/archive.sh",
			"Valheim/lib/game.sh",
			"Valheim/lib/compose_mods.py",
			"Valheim/ops/install-mod.sh",
			"Valheim/ops/delete-mod.sh",
			"Valheim/ops/restart.sh",
			"Valheim/ops/archive-world.sh",
		},

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

		InstallScript: "VintageStory/install.sh",
		Scripts: []string{
			"VintageStory/status_agent.py",
			"VintageStory/apply_config.sh",
			"controller.sh",
			"lib/archive.sh",
			"VintageStory/lib/game.sh",
			"VintageStory/lib/mods.sh",
			"VintageStory/lib/world.sh",
			"VintageStory/lib/update-game.sh",
			"VintageStory/ops/install-mod.sh",
			"VintageStory/ops/delete-mod.sh",
			"VintageStory/ops/restart.sh",
			"VintageStory/ops/update.sh",
			"VintageStory/ops/set-player-role.sh",
			"VintageStory/ops/archive-world.sh",
		},

		DataPath:               "/home/vintagestory/data",
		WorldDownloadPaths:     "Saves,Mods,Playerdata",
		WorldDownloadCorePaths: "Saves",

		DefaultInstanceType:  "t3a.medium",
		DefaultVolumeSizeGib: 10,

		RequiresGameVersion: true,
	},
}
