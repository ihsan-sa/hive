package work

// The tests run on whatever disk the machine has: its IO pressure mustn't
// hold their workers or checks. TestSaturatedDiskHoldsNewWorkersAndChecks
// sets its own.
func init() { diskBusy = func(float64) string { return "" } }
