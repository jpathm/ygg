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
