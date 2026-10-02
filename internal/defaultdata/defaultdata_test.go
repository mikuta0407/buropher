package defaultdata

import "testing"

func TestBuild(t *testing.T) {
	p := Build(func(k string) string { return k }, true)
	// Redmine 6.1.2 で default data を投入した DB の WorkflowTransition 件数と一致すること
	if n := len(p.Transitions); n != 144 {
		t.Errorf("transitions = %d", n)
	}
	if len(p.Roles[0].Permissions) != 76 {
		t.Errorf("manager permissions = %d", len(p.Roles[0].Permissions))
	}
	if len(Build(func(k string) string { return k }, false).Transitions) != 0 {
		t.Error("workflow=false should not create transitions")
	}
}
