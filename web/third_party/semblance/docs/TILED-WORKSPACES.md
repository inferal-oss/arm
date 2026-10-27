# Tiled workspaces

`c:TiledWorkspace` is a tree of panes the user rearranges. Its `c:rootTile` is a `c:SplitView`
(or a single leaf); each split view's `c:primary` and `c:secondary` are split views again or
leaves, usually sortable `c:Tabs`. Nothing in it is new layout: the split views size and stack
their panes (each divider is a `role=separator` with its own `c:ratio`), and the tabs show theirs.
What the workspace adds is that the tree is the user's to change, by dragging tabs.

```turtle
@prefix c: <https://semblance.dev/ns/component#> .
@prefix dnd: <https://semblance.dev/ns/dragdrop#> .
@prefix ui: <https://example.com/ui#> .

ui:workspace a c:TiledWorkspace ; c:label "Workspace" ; c:rootTile ui:columns .
ui:columns a c:SplitView ; c:primary ui:main ; c:secondary ui:side .
ui:main a c:Tabs , dnd:Sortable ; c:tabs ( ui:editor ui:preview ) ; c:selected ui:editor .
ui:side a c:Tabs , dnd:Sortable ; c:tabs ( ui:notes ) .
ui:editor a c:Tab ; c:label "Editor" ; c:slot "editor" .
ui:preview a c:Tab ; c:label "Preview" ; c:content ui:previewPane .
ui:notes a c:Tab ; c:label "Notes" ; c:content ui:notesPane .
```

## Rearranging

Sortable tabs (`c:Tabs` that are also `dnd:Sortable`) use the drag and drop layer
([dragdrop.ttl](../ontology/layers/dragdrop.ttl)): each tab's button is the root of a
`c:TabHandle`, a draggable carrying the tab. By pointer, a tab is dragged; by keyboard, Space picks
it up, the arrow keys go between the panes and their edges, and Space or Enter puts it down (the
drag's status, in a live region, names where it would go). In a workspace each pane has five drop
zones: the whole pane, and a strip along each edge that shows, while the tab is over it, the half
of the pane the new one would take.

- **Onto tabs**: the tab moves there, last, and is selected (`c:TabsReceive`). The tabs it left
  drop it from their list and their selection (`c:TabsTake`), so they show their first tab.
- **Onto an edge of a pane**: a new split view takes the pane's place in the tree, the pane on one
  side and new sortable tabs holding the tab on the other, before it at the start or top, after it
  at the end or bottom, stacked for the top and bottom (`c:TiledWorkspaceSplit`). Both are minted
  under the pane from the drop event (`<pane>/~split-<n>`, `<pane>/~pane-<n>`), so splitting
  twice never reuses a node, and the new tabs open their windows where the pane's do. Dropping a
  pane's only tab beside it changes nothing.
- **Away from tabs that then have none**: `c:TabsTake` raises `c:CloseTile` on them, and
  `c:TiledWorkspaceClose` removes their split view, its other pane taking its place. Tabs a split
  minted go with it; the application's own keep their facts.

Every change is one reaction (a `CONSTRUCT` pair with a function twin) to one recorded event, and
each tab keeps its IRI, its content node and its slot throughout, so whatever a tab shows keeps
its state and its DOM.

## Keeping an arrangement

The tree is graph state, so a `per:Policy` over it keeps an arrangement. The gallery's example
keeps its for the browser tab:

```turtle
ui:rememberWorkspace a per:Policy ; per:scope per:Session ;
    per:construct """
CONSTRUCT { ui:workspace c:rootTile ?root . ?n ?p ?o . ?cell rdf:first ?tab ; rdf:rest ?rest }
WHERE {
  { ui:workspace c:rootTile ?root }
  UNION { ui:workspace c:rootTile/(c:primary|c:secondary)* ?n . ?n ?p ?o
          FILTER(?p IN (rdf:type, c:primary, c:secondary, c:ratio, pos:direction, c:tabs, c:selected)) }
  UNION { ui:workspace c:rootTile/(c:primary|c:secondary)* ?n . ?n c:tabs/rdf:rest* ?cell . ?cell rdf:first ?tab ; rdf:rest ?rest }
}""" .
```

Restored, each subject's properties replace what the application starts with.

## On a phone

At compact width each split view stacks its panes, so the workspace is a column of panes, each as
tall as what it shows (a few lines at least), and the dividers go. Tabs still move between panes and split them by
dragging (a long press picks a tab up) or by keyboard.
