---
version: alpha
colors:
  background: "#202327"
  surface: "#292d32"
  primary: "#8297aa"
  text: "#d8dde2"
  muted: "#a3adb7"
typography:
  sans:
    fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif'
rounded:
  card: "12px"
  control: "8px"
omitted:
  - section: spacing
    reason: "The lightweight native Web and Android views own their runtime spacing."
  - section: components
    reason: "Vanilla DOM and Android platform widgets remain canonical."
---
# NodexLM — quiet graphite

## Product and audience

NodexLM is an experimental local GGUF inference cluster built around a Host,
authenticated Guests and llama.cpp RPC. It is an operational console, not an
AI chat subscription service. The interface must prioritize real device state,
model selection, capacity estimates, inference and troubleshooting.

## Shared visual language

- Matte charcoal canvas (`#202327`), neutral elevated surfaces (`#292d32`)
  and subtle separators (`#3b4147`).
- Text uses soft off-white and gray; actions and focus use muted steel-blue
  (`#8297aa` and related shades). Avoid neon, green decorative accents,
  bright gradients, glassmorphism, heavy shadows and marketing hero graphics.
- Flat network mark: five connected nodes form a subtle N, representing
  one Host coordinating multiple devices. Web uses inline SVG; Android uses
  a small native Canvas view. Both must remain readable at compact sizes.
- Android launcher uses the same five-node N: adaptive foreground (within
  the 66dp circular safe zone), flat graphite background, round launcher icon
  and a system-tinted monochrome variant on Android 13+. Manifest refers
  explicitly to the regular and round mipmap resources.
- Corners: 12px for panels, 8–9px for controls. Prefer borders to elevation.
- Keep semantic statuses understandable by text and shape, never color alone.
  Critical failure/confirmation states may use semantic warning colors when
  required by accessibility.

## Web console

The canonical Web assets live in `internal/dashboard/static`. Keep vanilla
HTML/CSS/JavaScript and existing authenticated API endpoints: no new frontend
framework, remote fonts or client dependency for the redesign.

The six existing workspaces are the source of truth:
Visão geral, Modelos, Inferência, Dispositivos, Diagnóstico and Chat. The
overview has working shortcuts and real Host/Guest/model state; never display
invented session history, token rates or aggregate usable VRAM. Capacity
and placement remain estimates until validated by the runtime.

Navigation must keep keyboard arrow/Home/End support, visible focus and
the active workspace in session state. Preserve the existing forms, model
downloads, GGUF inspection, load/unload, pairing, measurements and chat
stream/cancel behavior. Long lists and conversation history own bounded scroll.

## Android

The Android 10+ interface stays native Kotlin views without WebView or a
Flutter runtime. The Host, Guest RPC and standalone worker behaviors are
unchanged. MainActivity separates five functions:

1. Visão geral — device summary and shortcuts.
2. Conexão — Host URL, pairing code, device identity and trusted certificate.
3. Processamento — backend choice, RPC Guest start/stop and standalone worker.
4. Modelos — search, download/import, progress and local selection.
5. Ajustes — energy limits, maintenance, update checks and notices.

Tabs use an accessible, horizontally scrollable rail on narrow screens.
Keep real background services, notifications, authentication and file APIs;
do not imitate capabilities the Android runtime does not provide.

## Interaction quality

Preserve typed drafts after a failed request. Cancel actual in-flight work
rather than hiding a spinner. Use native buttons, labels, keyboard navigation,
text progress/error messages, local-only token storage and visible scrollbars.
Respect reduced motion and avoid decorative animations. No text or activity
metrics should claim an unmeasured result.
