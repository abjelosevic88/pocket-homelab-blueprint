// A cytoscape canvas: nodes and edges in, taps out. Layered (dagre) or organic (fcose) layout.
import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import cytoscape, { type Core, type ElementDefinition } from 'cytoscape'
import dagre from 'cytoscape-dagre'
import fcose from 'cytoscape-fcose'
import { useApp } from '../lib/app-context'

cytoscape.use(dagre)
cytoscape.use(fcose)

export interface GraphHandle { fit: () => void; png: () => string | null; center: (id: string) => void }

export interface CanvasNode { id: string; label: string; color: string; size: number; parent?: string; shape?: string; cycle?: boolean; dim?: boolean; focus?: boolean; kind?: string }
export interface CanvasEdge { from: string; to: string; width: number; cycle?: boolean; label?: string }

// cytoscape needs non-empty ids; the top-level folder is "" in a scan
const cid = (id: string) => id === '' ? '.' : id
const back = (id: string) => id === '.' ? '' : id

function css(name: string) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888' }

export const GraphCanvas = forwardRef<GraphHandle, {
  nodes: CanvasNode[]; edges: CanvasEdge[]; layout: 'layers' | 'organic'; direction?: 'TB' | 'LR'
  onTap?: (id: string) => void; onDblTap?: (id: string) => void; height?: number | string; tableMode?: boolean
}>(function GraphCanvas({ nodes, edges, layout, direction = 'TB', onTap, onDblTap, height = 560, tableMode }, ref) {
  const box = useRef<HTMLDivElement>(null)
  const cy = useRef<Core | null>(null)
  const taps = useRef({ onTap, onDblTap })
  taps.current = { onTap, onDblTap }
  const { theme } = useApp()

  useImperativeHandle(ref, () => ({
    fit: () => cy.current?.fit(undefined, 30),
    png: () => cy.current ? cy.current.png({ full: true, scale: 2, bg: css('--surface') }) : null,
    center: (id: string) => { const n = cy.current?.getElementById(cid(id)); if (n && n.length) cy.current?.animate({ center: { eles: n }, zoom: Math.max(cy.current.zoom(), 1) }, { duration: 250 }) },
  }))

  useEffect(() => {
    if (!box.current) return
    const text = css('--text'), muted = css('--muted'), border = css('--border-strong'), surface = css('--surface'), danger = css('--danger'), accent = css('--accent')
    const els: ElementDefinition[] = [
      ...nodes.map(n => ({ data: { id: cid(n.id), label: n.label, color: n.color, size: n.size, parent: n.parent === undefined ? undefined : cid(n.parent), kind: n.kind || '' }, classes: [n.cycle ? 'cycle' : '', n.dim ? 'dim' : '', n.focus ? 'focus' : '', n.shape === 'group' ? 'group' : ''].join(' ') })),
      ...edges.map((e, i) => ({ data: { id: `e${i}`, source: cid(e.from), target: cid(e.to), width: e.width, label: e.label || '' }, classes: e.cycle ? 'cycle' : '' })),
    ]
    const c = cytoscape({
      container: box.current,
      elements: els,
      minZoom: 0.08,
      maxZoom: 3,
      style: [
        { selector: 'node', style: {
          'background-color': 'data(color)', width: 'data(size)', height: 'data(size)', label: 'data(label)', color: text, 'font-size': 11, 'font-family': 'Inter, system-ui, sans-serif',
          'text-valign': 'bottom', 'text-margin-y': 4, 'text-outline-color': surface, 'text-outline-width': 2.5, 'border-width': 0,
          shape: tableMode ? 'round-rectangle' : 'ellipse',
        } },
        { selector: 'node.group', style: { 'background-opacity': 0.05, 'background-color': muted, 'border-width': 1, 'border-color': border, 'border-style': 'dashed', label: 'data(label)', 'text-valign': 'top', 'text-halign': 'center', 'font-size': 10, color: muted, shape: 'round-rectangle', padding: '14px' } },
        { selector: 'node.cycle', style: { 'border-width': 2.5, 'border-color': danger } },
        { selector: 'node.focus', style: { 'border-width': 3, 'border-color': accent } },
        { selector: 'node:selected', style: { 'border-width': 3, 'border-color': accent } },
        { selector: 'edge', style: {
          width: 'data(width)', 'line-color': border, 'target-arrow-color': border, 'target-arrow-shape': 'triangle', 'arrow-scale': 0.8, 'curve-style': 'bezier', opacity: 0.75,
          label: 'data(label)', 'font-size': 9, color: muted, 'text-rotation': 'autorotate', 'text-outline-color': surface, 'text-outline-width': 2,
        } },
        { selector: 'edge.cycle', style: { 'line-color': danger, 'target-arrow-color': danger, opacity: 0.9 } },
        { selector: '.faded', style: { opacity: 0.12 } },
        { selector: 'node.hi', style: { 'border-width': 2, 'border-color': accent } },
        { selector: 'edge.hi', style: { 'line-color': accent, 'target-arrow-color': accent, opacity: 1 } },
      ],
      layout: layoutOf(layout, direction, nodes.length),
    })
    c.on('tap', 'node', e => taps.current.onTap?.(back(e.target.id())))
    c.on('dbltap', 'node', e => taps.current.onDblTap?.(back(e.target.id())))
    c.on('mouseover', 'node', e => {
      const n = e.target
      if (n.hasClass('group')) return
      const near = n.closedNeighborhood()
      c.elements().not(near).not(near.parents()).addClass('faded')
      near.addClass('hi')
      n.removeClass('hi')
    })
    c.on('mouseout', 'node', () => c.elements().removeClass('faded hi'))
    cy.current = c
    return () => { c.destroy(); cy.current = null }
  }, [nodes, edges, layout, direction, theme, tableMode])

  return <div ref={box} className="graph-canvas" style={{ height }} />
})

function layoutOf(layout: 'layers' | 'organic', direction: 'TB' | 'LR', n: number): cytoscape.LayoutOptions {
  if (layout === 'layers') return { name: 'dagre', rankDir: direction, nodeSep: 28, rankSep: 70, edgeSep: 10, animate: false, fit: true, padding: 30 } as cytoscape.LayoutOptions
  return { name: 'fcose', quality: n > 400 ? 'draft' : 'default', animate: false, randomize: true, nodeRepulsion: 9000, idealEdgeLength: 90, nestingFactor: 0.6, padding: 30, fit: true } as cytoscape.LayoutOptions
}
