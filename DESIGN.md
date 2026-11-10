---
version: alpha
colors:
  background: "#101318"
  surface: "#191f27"
  primary: "#3477ad"
  text: "#e1e6ed"
  muted: "#98a7ba"
typography:
  sans:
    fontFamily: 'Inter, system-ui, -apple-system, "Segoe UI", sans-serif'
rounded:
  card: "15px"
  control: "8px"
omitted:
  - section: spacing
    reason: "Existing console CSS remains the canonical spacing source."
  - section: components
    reason: "Vanilla DOM helpers and native controls own the small console."
---
# NodexLM console

## Overview

A Portuguese operational console for an experimental local inference cluster. Preserve its existing dark graphite panels, blue actions, readable resource lists and native controls. The product should feel like a local control panel, with actual connection/loading state taking precedence over decoration. Do not apply a marketing landing-page layout.

## Colors

The canonical runtime values live in `internal/dashboard/static/styles.css`; the frontmatter records the existing palette. Blue denotes the primary action, green a ready/completed state, and amber an advisory capacity estimate. Never present an estimate as measured model throughput. Scrollbar tokens `--scrollbar-thumb` and `--scrollbar-track` apply globally.

## Typography

Use the existing system-font stack and Portuguese copy. Let long GGUF filenames wrap. Do not fetch remote fonts or add a web framework for one control.

## Layout

The operational Console uses a compact left navigation rail on desktop and horizontally scrollable menu on smaller screens. Each of six functional workspaces has its own short content; the active workspace and long data lists own bounded scrolling rather than stacking all functions into one long page. Tables still own horizontal overflow and chat owns the bounded conversation log. The existing runtime CSS is canonical for spacing and layout tokens.

## Elevation & Depth

Preserve the existing subtle panel elevation and borders. Dialogs use the browser's native top layer and a dark backdrop.

## Shapes

Panels use 15px corners; controls use 8px corners. Resource tags retain their established pill shape.

## Components

Shared DOM helpers `el`, `action` and `notice` in `app.js` own content, actions and feedback. Use textContent for external data. Native dialog owns modal focus/Escape behavior; the shared confirmAction function owns confirmation outcomes. Native labels, selects, number inputs and buttons remain canonical. Workspace navigation uses buttons, aria-current and arrow/Home/End keys, retaining the single-page state without introducing another UI framework.

## Do's and Don'ts

Keep visible focus indicators, operable scrollbars and reduced-motion support. Preserve typed messages on failure. Stop must cancel the real fetch. Show elapsed time and first-token time from browser measurements; show tokens/s only with a real completion-token count. Do not render model output as HTML.
