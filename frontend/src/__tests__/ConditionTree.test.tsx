import { render, screen, fireEvent } from '@testing-library/react'
import { ConditionTree } from '../components/ConditionTree'
import type { GroupNode, ConditionLeaf } from '../types'

test('renders AND group with action buttons', () => {
  const root: GroupNode = { type: 'AND', children: [] }
  render(<ConditionTree value={root} onChange={() => {}} />)
  expect(screen.getByText('AND')).toBeInTheDocument()
  expect(screen.getByText('+ Condition')).toBeInTheDocument()
  expect(screen.getByText('+ Group')).toBeInTheDocument()
})

test('toggles AND to OR when group label clicked', () => {
  const root: GroupNode = { type: 'AND', children: [] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.click(screen.getByText('AND'))
  expect(onChange).toHaveBeenCalledWith({ type: 'OR', children: [] })
})

test('adds a default condition when + Condition clicked', () => {
  const root: GroupNode = { type: 'AND', children: [] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.click(screen.getByText('+ Condition'))
  expect(onChange).toHaveBeenCalledWith({
    type: 'AND',
    children: [
      {
        type: 'condition',
        indicator: 'RSI',
        params: { period: 14 },
        operator: '<',
        value: 50,
      },
    ],
  })
})

test('adds a nested group when + Group clicked', () => {
  const root: GroupNode = { type: 'AND', children: [] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.click(screen.getByText('+ Group'))
  expect(onChange).toHaveBeenCalledWith({
    type: 'AND',
    children: [{ type: 'OR', children: [] }],
  })
})

test('removes a child condition when Remove clicked', () => {
  const leaf: ConditionLeaf = {
    type: 'condition',
    indicator: 'RSI',
    params: { period: 14 },
    operator: '<',
    value: 50,
  }
  const root: GroupNode = { type: 'AND', children: [leaf] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.click(screen.getByText('Remove'))
  expect(onChange).toHaveBeenCalledWith({ type: 'AND', children: [] })
})

test('updates indicator select on change', () => {
  const leaf: ConditionLeaf = {
    type: 'condition',
    indicator: 'RSI',
    params: { period: 14 },
    operator: '<',
    value: 50,
  }
  const root: GroupNode = { type: 'AND', children: [leaf] }
  const onChange = vi.fn()
  render(<ConditionTree value={root} onChange={onChange} />)
  fireEvent.change(screen.getByDisplayValue('RSI'), { target: { value: 'EMA' } })
  expect(onChange).toHaveBeenCalledWith({
    type: 'AND',
    children: [{ ...leaf, indicator: 'EMA' }],
  })
})
