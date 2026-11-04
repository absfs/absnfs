//go:build go1.25 && unix

package absnfs

// basefsConfines is whether basefs over osfs confines itself to its root
// with os.Root in this build, as osfs's OpenRoot does with Go 1.25 or later
// on a Unix system: the stack c4d serves workspaces from.
const basefsConfines = true
