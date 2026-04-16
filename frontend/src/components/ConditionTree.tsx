import type { ConditionNode, GroupNode, ConditionLeaf } from '../types'

const INDICATORS = ['RSI', 'MACD', 'EMA', 'SMA', 'BB', 'Volume', 'ATR', 'Stochastic']
const OPERATORS = ['<', '>', '=', '!=', 'crosses_above', 'crosses_below', '% change >', 'relative_to']

// --- Leaf ---

interface LeafViewProps {
  node: ConditionLeaf
  onChange: (n: ConditionNode) => void
  onRemove: () => void
}

function LeafView({ node, onChange, onRemove }: LeafViewProps) {
  return (
    <div className="flex flex-wrap items-center gap-2 p-2 bg-white border rounded text-sm">
      <select
        value={node.indicator}
        onChange={(e) => onChange({ ...node, indicator: e.target.value })}
        className="border rounded px-2 py-1"
      >
        {INDICATORS.map((i) => <option key={i}>{i}</option>)}
      </select>
      <input
        type="number"
        placeholder="period"
        value={node.params.period ?? ''}
        onChange={(e) =>
          onChange({ ...node, params: { ...node.params, period: Number(e.target.value) } })
        }
        className="border rounded px-2 py-1 w-20"
      />
      <select
        value={node.operator}
        onChange={(e) => onChange({ ...node, operator: e.target.value })}
        className="border rounded px-2 py-1"
      >
        {OPERATORS.map((o) => <option key={o}>{o}</option>)}
      </select>
      <input
        type="number"
        placeholder="value"
        value={node.value ?? ''}
        onChange={(e) => onChange({ ...node, value: Number(e.target.value) })}
        className="border rounded px-2 py-1 w-24"
      />
      <button
        onClick={onRemove}
        className="ml-auto text-red-500 hover:text-red-700 text-xs"
      >
        Remove
      </button>
    </div>
  )
}

// --- Group ---

interface GroupViewProps {
  node: GroupNode
  onChange: (n: GroupNode) => void
  onRemove?: () => void
}

function GroupView({ node, onChange, onRemove }: GroupViewProps) {
  function toggleType() {
    onChange({ ...node, type: node.type === 'AND' ? 'OR' : 'AND' })
  }

  function addCondition() {
    const newLeaf: ConditionLeaf = {
      type: 'condition',
      indicator: 'RSI',
      params: { period: 14 },
      operator: '<',
      value: 50,
    }
    onChange({ ...node, children: [...node.children, newLeaf] })
  }

  function addGroup() {
    const newGroup: GroupNode = { type: 'OR', children: [] }
    onChange({ ...node, children: [...node.children, newGroup] })
  }

  function updateChild(idx: number) {
    return (child: ConditionNode) => {
      const children = [...node.children]
      children[idx] = child
      onChange({ ...node, children })
    }
  }

  function removeChild(idx: number) {
    return () => onChange({ ...node, children: node.children.filter((_, i) => i !== idx) })
  }

  return (
    <div className="border-l-2 border-blue-300 pl-3 space-y-2">
      <div className="flex items-center gap-2">
        <button
          onClick={toggleType}
          className="px-2 py-0.5 rounded bg-blue-100 text-blue-700 text-xs font-bold hover:bg-blue-200"
        >
          {node.type}
        </button>
        {onRemove && (
          <button
            onClick={onRemove}
            className="text-red-500 hover:text-red-700 text-xs"
          >
            Remove group
          </button>
        )}
      </div>
      <div className="space-y-2 pl-2">
        {node.children.map((child, idx) => (
          <NodeView
            key={idx}
            node={child}
            onChange={updateChild(idx)}
            onRemove={removeChild(idx)}
          />
        ))}
      </div>
      <div className="flex gap-2 pt-1">
        <button
          onClick={addCondition}
          className="text-xs text-blue-600 hover:underline"
        >
          + Condition
        </button>
        <button
          onClick={addGroup}
          className="text-xs text-blue-600 hover:underline"
        >
          + Group
        </button>
      </div>
    </div>
  )
}

// --- Dispatcher ---

interface NodeViewProps {
  node: ConditionNode
  onChange: (n: ConditionNode) => void
  onRemove?: () => void
}

function NodeView({ node, onChange, onRemove }: NodeViewProps) {
  if (node.type === 'AND' || node.type === 'OR') {
    return (
      <GroupView
        node={node}
        onChange={(n) => onChange(n)}
        onRemove={onRemove}
      />
    )
  }
  if (node.type === 'condition') {
    return (
      <LeafView node={node} onChange={onChange} onRemove={onRemove!} />
    )
  }
  // signal_ref nodes are not yet editable in the UI
  return null
}

// --- Public export ---

interface ConditionTreeProps {
  value: ConditionNode
  onChange: (v: ConditionNode) => void
}

export function ConditionTree({ value, onChange }: ConditionTreeProps) {
  // Ensure root is always a group
  const root: GroupNode =
    value.type === 'AND' || value.type === 'OR'
      ? (value as GroupNode)
      : { type: 'AND', children: [value] }

  return <GroupView node={root} onChange={onChange} />
}
