import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import {
  Sidebar,
  SidebarFooter,
  SidebarHeader,
  SidebarItem,
  SidebarNav,
  SidebarSection,
  SidebarToggle,
} from '@/components/ui/sidebar'

function renderSidebar() {
  return render(
    <Sidebar variant="collapsible" width={228} aria-label="Main">
      <SidebarHeader>
        <span>KineRouter</span>
        <SidebarToggle />
      </SidebarHeader>
      <SidebarNav>
        <SidebarSection label="Gateway">
          <SidebarItem icon={<span>i</span>} active>
            Endpoint &amp; Keys
          </SidebarItem>
        </SidebarSection>
      </SidebarNav>
      <SidebarFooter>
        <span>v0.1.9</span>
      </SidebarFooter>
    </Sidebar>,
  )
}

describe('ui/sidebar', () => {
  it('collapses to the icon rail and hides labels', () => {
    const { container } = renderSidebar()
    const rail = screen.getByRole('navigation', { name: 'Main' })
    expect(rail.dataset.collapsed).toBe('false')
    expect(screen.getByText('Endpoint & Keys')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: 'Collapse sidebar' }))

    expect(rail.dataset.collapsed).toBe('true')
    expect(rail.style.width).toBe('60px')
    // Labels animate out; the rail falls back to a native title for the icon.
    expect(screen.getByText('Endpoint & Keys').closest('[data-slot="sidebar-item"]')?.getAttribute('title'))
      .toBe('Endpoint & Keys')
    expect(container.querySelector('[data-slot="sidebar-item-icon"]')).not.toBeNull()
  })
})
