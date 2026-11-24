package agy

// stubPool stands in for the warm pool the upstream tests isolate. This port
// spawns cold per turn, so there is nothing to close; the helper keeps the
// upstream test bodies untouched.
type stubPool struct{}

func (*stubPool) Close() {}

func isolatedPoolDeferred(t interface {
	Cleanup(func())
	Helper()
}) *stubPool {
	t.Helper()
	return &stubPool{}
}
