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
