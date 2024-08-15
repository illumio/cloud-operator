package version

var (
	version = "dev"
)

// Version returns the version of the operator.
func Version() string {
	return version
}
