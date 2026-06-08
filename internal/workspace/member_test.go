package workspace

import "testing"

func TestIsValidRole(t *testing.T) {
	good := []string{RoleOwner, RoleAdmin, RoleMember}
	for _, r := range good {
		if !IsValidRole(r) {
			t.Errorf("%q should be valid", r)
		}
	}
	// reviewer/viewer were retired (epic:user-admin, migration 0027) and
	// must now be rejected by the validator.
	bad := []string{"", "reviewer", "viewer", "Admin", "ADMIN", "guest"}
	for _, r := range bad {
		if IsValidRole(r) {
			t.Errorf("%q should be invalid", r)
		}
	}
}
