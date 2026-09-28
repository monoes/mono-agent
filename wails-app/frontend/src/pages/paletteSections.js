// The node palette is split in two: the actions of the installed web
// automations, and every other node. Which section a node belongs to comes
// from `node palette` (each node's `section`); the palette only groups.

export const WEB_AUTOMATION = 'web_automation'

// paletteSectionOf is a category's section, from its nodes.
export function paletteSectionOf(nodes) {
  return Array.isArray(nodes) && nodes.some(n => n?.section === WEB_AUTOMATION) ? WEB_AUTOMATION : 'nodes'
}

// paletteCategoryLabel is the automation's name for a web automation
// category, '' for a built-in one.
export function paletteCategoryLabel(nodes) {
  return (Array.isArray(nodes) && nodes.find(n => n?.category_label)?.category_label) || ''
}

// splitPaletteSections groups categories into the two sections, web
// automations first (sorted by name) and then the rest in their given order.
// Both sections are always returned, so an empty one can say so.
export function splitPaletteSections(categories) {
  const web = [], rest = []
  for (const c of categories || []) (c.section === WEB_AUTOMATION ? web : rest).push(c)
  web.sort((a, b) => String(a.label).localeCompare(String(b.label)))
  return [
    { id: WEB_AUTOMATION, label: 'Web automations', categories: web },
    { id: 'nodes', label: 'Nodes', categories: rest },
  ]
}
