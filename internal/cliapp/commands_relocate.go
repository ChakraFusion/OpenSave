package cliapp

import (
	"fmt"
	"net/url"
	"os"
)

const relocateUsage = `usage: opensave relocate <gameId> use        use the folder offered for a missing save folder
       opensave relocate <gameId> dismiss    not that one — it is not offered again while OpenSave runs`

// cmdRelocate answers the offer the daemon makes when a game's save folder is
// missing and the same path exists on another drive here: use that folder, or
// not. Never switched without this answer — it could be an old copy.
func cmdRelocate(args []string) int {
	asJSON, args := jsonFlag(args)
	if len(args) != 2 || (args[1] != "use" && args[1] != "dismiss") {
		fmt.Fprintln(os.Stderr, relocateUsage)
		return 1
	}
	gameID := args[0]
	found := runningDaemonRelocations()[gameID]
	if found == "" {
		return fail(asJSON, fmt.Errorf("no folder is offered for %s (OpenSave must be running, and the save folder missing)", gameID))
	}
	path := "/api/games/" + url.PathEscape(gameID) + "/relocate"
	var body any = map[string]string{"path": found}
	if args[1] == "dismiss" {
		path, body = path+"/dismiss", nil
	}
	raw, err := daemonRequest("POST", path, body)
	if err != nil {
		return fail(asJSON, err)
	}
	if asJSON {
		return emitRawJSON(raw)
	}
	if args[1] == "use" {
		success("%s now uses %s on this device.", bold(gameID), found)
	} else {
		success("Not using %s for %s.", found, bold(gameID))
	}
	return 0
}
