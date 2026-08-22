package auth

// DispatchPolicy selects which usage windows fence a request.
// Standard models keep the existing 5h/7d account-level gates.
// Spark requests ignore those gates and only look at the independent spark window.
type DispatchPolicy int

const (
	DispatchPolicyStandard DispatchPolicy = iota
	DispatchPolicySpark
)
