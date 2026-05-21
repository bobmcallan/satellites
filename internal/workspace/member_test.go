package workspace

import "testing"

func TestIsValidRole(t *testing.T) {
	good := []string{RoleAdmin, RoleMember, RoleReviewer, RoleViewer}
	for _, r := range good {
		if !IsValidRole(r) {
			t.Errorf("%q should be valid", r)
		}
	}
	bad := []string{"", "owner", "Admin", "ADMIN", "guest"}
	for _, r := range bad {
		if IsValidRole(r) {
			t.Errorf("%q should be invalid", r)
		}
	}
}
