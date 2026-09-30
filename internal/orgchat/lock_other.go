//go:build !unix

package orgchat

import "context"

// lockPath takes the lock at path. Without flock it is a claim file next
// to it (claim.go).
func lockPath(ctx context.Context, path string) (func(), error) {
	return claimLock(ctx, path+".claim")
}
