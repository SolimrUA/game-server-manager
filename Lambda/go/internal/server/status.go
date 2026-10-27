package server

// GameStatus is what the server last reported about the game it runs: player count, version, mods. Kept
// as a map because the control panel passes it through to the front end unchanged, and each game's
// status agent reports its own fields.
type GameStatus map[string]any

// Players reports how many players the server last said were connected, and whether it said at all.
// A game that isn't answering reports nothing, which reads the same as nobody playing.
func (g GameStatus) Players() (int, bool) {
	players, ok := g["players"].(map[string]any)
	if !ok {
		return 0, false
	}
	current, ok := players["current"].(float64)
	if !ok {
		return 0, false
	}
	return int(current), true
}

// ServerName is the name the game reports for itself, which the front end shows in its own column.
func (g GameStatus) ServerName() string {
	name, _ := g["serverName"].(string)
	return name
}

// Version is the game version the server reports running.
func (g GameStatus) Version() string {
	version, _ := g["version"].(string)
	return version
}
