package localfile

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigDACL(t *testing.T) {
	owner, err := windows.StringToSid("S-1-5-21-1-2-3-1001")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, acl string
		allowed   bool
	}{
		{"trusted writers", "D:(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;S-1-5-21-1-2-3-1001)(A;;FR;;;WD)", true},
		{"generic write", "D:(A;;GW;;;WD)", false},
		{"append", "D:(A;;0x4;;;AU)", false},
		{"change permissions", "D:(A;;WD;;;BU)", false},
		{"null DACL", "D:NO_ACCESS_CONTROL", false},
		{"missing DACL", "O:SY", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(c.acl)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkConfigDACL(sd, owner); (err == nil) != c.allowed {
				t.Fatalf("allowed = %v, error = %v", err == nil, err)
			}
		})
	}
}

func TestConfigSecurityWindowsAdministratorsOwner(t *testing.T) {
	const userSID = "S-1-5-21-1-2-3-1001"
	const otherSID = "S-1-5-21-1-2-3-1002"
	user, err := windows.StringToSid(userSID)
	if err != nil {
		t.Fatal(err)
	}
	const trustedDACL = "D:(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + userSID + ")"
	for _, tc := range []struct {
		name, sddl string
		allowed    bool
	}{
		{"current user owns file", "O:" + userSID + trustedDACL, true},
		{"Administrators own file", "O:BA" + trustedDACL, true},
		{"another user owns file", "O:" + otherSID + trustedDACL, false},
		{"Administrators own file with untrusted writer", "O:BA" + trustedDACL + "(A;;GW;;;AU)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkConfigSecurity(sd, user); (err == nil) != tc.allowed {
				t.Fatalf("allowed = %v, error = %v", err == nil, err)
			}
		})
	}
}
