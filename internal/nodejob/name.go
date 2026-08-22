package nodejob

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	nodeJobPartLen = 24
	nodePartLen    = 20
	suffixLen      = 16
)

// JobName truncates the readable parts but digests the full ones, so truncation cannot
// collide. All three inputs must feed the digest: without nodeJobName, two NodeJobs
// sharing a jobTemplate and a 24-character name prefix collide on one node.
//
// The result must fit 63 characters, the limit for the batch.kubernetes.io/job-name label
// the Job controller copies onto pods: 24 + 1 + 20 + 1 + 16 = 62.
func JobName(nodeJobName, nodeName, runHash string) string {
	digest := sha256.Sum256([]byte(nodeJobName + "/" + nodeName + "/" + runHash))
	suffix := hex.EncodeToString(digest[:])[:suffixLen]

	nodeJobPart := sanitize(truncate(nodeJobName, nodeJobPartLen))
	if nodeJobPart == "" {
		nodeJobPart = "nodejob"
	}

	nodePart := sanitize(truncate(nodeName, nodePartLen))
	if nodePart == "" {
		nodePart = "node"
	}

	return strings.Join([]string{
		nodeJobPart,
		nodePart,
		suffix,
	}, "-")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	return strings.Trim(string(out), "-")
}
