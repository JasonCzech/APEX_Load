// Package winfs holds the Windows filesystem primitives that private,
// identity-bound and durable file ownership needs where POSIX modes, inodes
// and directory fsync do not exist: volume policy, long paths, write-through
// moves, private creation and validation through security descriptors,
// handle-based file identity, handle-relative rename, and identity-checked
// POSIX deletion. Every primitive is Windows-only; on other
// platforms the package is empty.
package winfs
