package cli

import (
	"strings"
	"testing"
)

func TestWorkspaceHelpMentionsHerdr(t *testing.T) {
	for name, help := range map[string]string{
		"new": newCmd.Long, "switch": switchCmd.Long,
	} {
		if !strings.Contains(help, "Herdr") {
			t.Errorf("%s help does not mention Herdr", name)
		}
	}
}

func TestNewHelpMentionsUnlinkedWarning(t *testing.T) {
	if !strings.Contains(newCmd.Long, "unlinked") {
		t.Error("new help does not explain the unlinked case")
	}
}
