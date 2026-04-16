// pkg/signals/condition.go
package signals

import (
	"encoding/json"
	"fmt"
)

// NodeType identifies the type of a condition tree node.
type NodeType string

const (
	NodeAND       NodeType = "AND"
	NodeOR        NodeType = "OR"
	NodeCondition NodeType = "condition"
	NodeSignalRef NodeType = "signal_ref"
)

// Node is the interface implemented by all tree nodes.
type Node interface {
	nodeType() NodeType
}

// ANDNode: all children must evaluate to true.
type ANDNode struct {
	Children []Node
}

func (n *ANDNode) nodeType() NodeType { return NodeAND }

// ORNode: at least one child must evaluate to true.
type ORNode struct {
	Children []Node
}

func (n *ORNode) nodeType() NodeType { return NodeOR }

// IndicatorRef names an indicator with its parameters.
type IndicatorRef struct {
	Indicator string             `json:"indicator"`
	Params    map[string]float64 `json:"params"`
}

// ConditionNode: compares an indicator value at candle[i] against a constant or another indicator.
type ConditionNode struct {
	Indicator string             // e.g. "RSI"
	Params    map[string]float64 // e.g. {"period": 14}
	Operator  string             // "<", ">", "=", "!=", "crosses_above", "crosses_below"
	Value     *float64           // compare against constant (mutually exclusive with CompareTo)
	CompareTo *IndicatorRef      // compare against another indicator's value
}

func (n *ConditionNode) nodeType() NodeType { return NodeCondition }

// SignalRefNode: delegates evaluation to another saved signal.
type SignalRefNode struct {
	SignalID string
}

func (n *SignalRefNode) nodeType() NodeType { return NodeSignalRef }

// rawNode is used for two-pass JSON parsing.
type rawNode struct {
	Type      string             `json:"type"`
	Children  []json.RawMessage  `json:"children"`
	Indicator string             `json:"indicator"`
	Params    map[string]float64 `json:"params"`
	Operator  string             `json:"operator"`
	Value     *float64           `json:"value"`
	CompareTo *IndicatorRef      `json:"compare_to"`
	SignalID  string             `json:"signal_id"`
}

// ParseConditions deserialises a JSONB conditions field into a Node tree.
func ParseConditions(data []byte) (Node, error) {
	var raw rawNode
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("signals: parse conditions: %w", err)
	}
	return parseNode(raw)
}

func parseNode(raw rawNode) (Node, error) {
	switch NodeType(raw.Type) {
	case NodeAND:
		children, err := parseChildren(raw.Children)
		if err != nil {
			return nil, err
		}
		return &ANDNode{Children: children}, nil

	case NodeOR:
		children, err := parseChildren(raw.Children)
		if err != nil {
			return nil, err
		}
		return &ORNode{Children: children}, nil

	case NodeCondition:
		return &ConditionNode{
			Indicator: raw.Indicator,
			Params:    raw.Params,
			Operator:  raw.Operator,
			Value:     raw.Value,
			CompareTo: raw.CompareTo,
		}, nil

	case NodeSignalRef:
		return &SignalRefNode{SignalID: raw.SignalID}, nil

	default:
		return nil, fmt.Errorf("signals: unknown node type %q", raw.Type)
	}
}

func parseChildren(raws []json.RawMessage) ([]Node, error) {
	nodes := make([]Node, 0, len(raws))
	for _, r := range raws {
		var raw rawNode
		if err := json.Unmarshal(r, &raw); err != nil {
			return nil, fmt.Errorf("signals: parse child: %w", err)
		}
		node, err := parseNode(raw)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}
