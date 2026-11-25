package agent

import (
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestDecide(t *testing.T) {
	opts := []acp.PermissionOption{
		{OptionId: "yes", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow"},
		{OptionId: "always", Kind: acp.PermissionOptionKindAllowAlways, Name: "Always"},
		{OptionId: "no", Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject"},
	}
	pick := func(o acp.RequestPermissionOutcome) string {
		if o.Selected != nil {
			return string(o.Selected.OptionId)
		}
		return "cancelled"
	}
	for kind, want := range map[acp.ToolKind]string{
		acp.ToolKindRead: "yes", acp.ToolKindSearch: "yes", acp.ToolKindThink: "yes",
		acp.ToolKindEdit: "no", acp.ToolKindDelete: "no", acp.ToolKindMove: "no",
		acp.ToolKindExecute: "no", acp.ToolKindFetch: "no", acp.ToolKindSwitchMode: "no", acp.ToolKindOther: "no",
	} {
		if got := pick(Decide(kind, opts)); got != want {
			t.Errorf("%s: got %s, want %s", kind, got, want)
		}
	}
	// Never "allow always", and with no reject option, cancel rather than allow.
	onlyAllow := opts[:2]
	if got := pick(Decide(acp.ToolKindEdit, onlyAllow)); got != "cancelled" {
		t.Errorf("edit with only allow options: %s", got)
	}
	if got := pick(Decide(acp.ToolKindRead, opts[1:2])); got != "cancelled" {
		t.Errorf("read with only allow-always: %s", got)
	}
}
