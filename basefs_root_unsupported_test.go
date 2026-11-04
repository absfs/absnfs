//go:build !(go1.25 && unix)

package absnfs

// basefsConfines is whether basefs over osfs confines itself to its root
// with os.Root in this build. osfs's OpenRoot needs Go 1.25 or later on a
// Unix system; elsewhere basefs constrains names only.
const basefsConfines = false
