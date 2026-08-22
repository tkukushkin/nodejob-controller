package nodejob

import (
	"regexp"
	"strings"
	"testing"
)

var dns1123Subdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func TestJobNameFitsSixtyThreeChars(t *testing.T) {
	name := JobName(
		strings.Repeat("nodejob", 10),
		"ip-10-0-1-23.eu-central-1.compute.internal",
		"abcdef0123456789",
	)
	if len(name) > 63 {
		t.Errorf("name %q is %d chars, want <= 63", name, len(name))
	}
	if !dns1123Subdomain.MatchString(name) {
		t.Errorf("name %q is not a valid DNS-1123 subdomain", name)
	}
}

func TestJobNameIsDeterministic(t *testing.T) {
	a := JobName("fix", "node-a", "abcdef0123456789")
	b := JobName("fix", "node-a", "abcdef0123456789")
	if a != b {
		t.Errorf("name not deterministic: %q != %q", a, b)
	}
}

func TestJobNameDistinguishesNodesWithSharedPrefix(t *testing.T) {
	prefix := "ip-10-0-1-23.eu-central-1.compute.internal"
	a := JobName("fix", prefix+"-one", "abcdef0123456789")
	b := JobName("fix", prefix+"-two", "abcdef0123456789")
	if a == b {
		t.Errorf("nodes sharing a long prefix collided on %q", a)
	}
}

func TestJobNameDistinguishesRunHashes(t *testing.T) {
	a := JobName("fix", "node-a", "1111111111111111")
	b := JobName("fix", "node-a", "2222222222222222")
	if a == b {
		t.Errorf("different run hashes collided on %q", a)
	}
}

func TestJobNameDistinguishesNodeJobsWithSharedPrefix(t *testing.T) {
	node := "ip-10-0-1-23.eu-central-1.compute.internal"
	a := JobName("fix-sysctl-somaxconn-zone-a", node, "abcdef0123456789")
	b := JobName("fix-sysctl-somaxconn-zone-b", node, "abcdef0123456789")
	if a == b {
		t.Errorf("NodeJobs sharing a 24-char name prefix collided on %q", a)
	}
}

func TestJobNameHandlesUnusableNodeName(t *testing.T) {
	name := JobName("fix", "...", "abcdef0123456789")
	if !dns1123Subdomain.MatchString(name) {
		t.Errorf("name %q is not a valid DNS-1123 subdomain", name)
	}
}
