package main

// rescueSignalTrigger describes a signal-based condition for the RescueBot
// partial-close trigger. Name is the signal name (e.g. "st-flip"); Params are
// passed to signal.ComputeStateForce as signal.Config.Params.
type rescueSignalTrigger struct {
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params,omitempty"`
}
