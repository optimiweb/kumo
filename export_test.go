//go:build integration

package kumo

// NewTestOperationID exposes operation ID generation for tests.
func NewTestOperationID() (OperationID, error) {
	return newOperationID()
}
