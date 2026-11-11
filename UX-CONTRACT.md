# Console behavior

Visual identity: [DESIGN.md](DESIGN.md). The console is loopback-only, authenticated with the admin token. Inference keys are accepted only at the two OpenAI inference routes.

| Capability | Canonical owner | Contract | Verification |
|---|---|---|---|
| Forms | Native form/label/input | Browser validation, preserve fields after errors | DOM regression and manual browser flow |
| Selects | Native select | Keyboard and platform-owned popup | Browser |
| Feedback | `notice` + inline status | Live-region announcements; durable errors remain inline | DOM assertions |
| Confirmation | `confirmAction` + native dialog | Cancel initially focused; Escape cancels; show object and consequence | Browser |
| Scrollbars | Global `styles.css` baseline | Visible and operable; table/chat own local overflow | Desktop/mobile browser |
| Chat | `sendChat` | One active generation, incremental text, real abort, failed turns excluded from saved history | Native E2E + stream cancellation regression |
| History | Browser localStorage | At most 62 messages; explicit confirmed deletion; no bearer tokens stored with history | Browser |
| Model load/download | Existing API state + polling | Keep UI responsive; show load phase/errors; partial transfers remain resumable | Go manager and shard tests |
| Console navigation | `activateConsoleView` + native buttons | Only one active workspace, keyboard arrow/Home/End navigation, aria-current, remembered selection | DOM regression + browser |
| Debug logs | CLI `--debug` + `internal/debuglog` | Explicit opt-in, local permissions 0600, bounded rotation, HTTP route only (no headers or request bodies) | Go privacy and rotation tests |

History is local to this browser/profile and can contain sensitive prompts. The interface labels its persistence and offers deletion. Logout clears the visible/in-memory transcript and aborts generation; saved history persists until explicitly erased. A partial/interrupted answer is visible but is not added to the next model context.

Runtime context is configured before loading. The chat sends prior completed turns within a 64-message request limit; a backend can still reject conversations exceeding the configured token window. Such errors retain the previous conversation and draft for recovery.

## Quiet graphite redesign invariants

- Web Visão geral shortcuts navigate into the six existing workspaces, without
  creating a parallel RPC/API path. The overview chat prompt only transfers
  a draft to the chat form; it does not submit or consume tokens.
- Overview capacity is the backend aggregate *estimated budget*, not guaranteed
  usable RAM/VRAM. Online nodes reflect the current authorized node list.
- Android: five horizontally scrollable tabs split device overview, Host
  connection, processing, GGUF library and energy/maintenance settings. Pair
  success may return to overview and offer RPC start. No service is started on
  a mere tab change.
- Web/Android share a quiet graphite, low-chroma steel-blue visual palette
  and a five-node N symbol. Neither variant adds WebView, UI frameworks, a
  telemetry service or changes the authentication protocol.

## Pareamento temporário — contagem regressiva

- O código de oito caracteres gerado na aba Dispositivos tem prazo de 10 minutos,
  informado pelo Host no retorno de `POST /v1/pairing/start` como
  `expires_in_ms` (além de `expires_in` para compatibilidade).
- O frontend mostra o prazo restante em `mm:ss`, reavaliando pelo relógio a
  cada segundo; após expirar, o código não pode ser apresentado como válido.
- O toggle **Renovar automaticamente ao expirar** começa desativado e somente
  renova um código previamente gerado pelo usuário, com sessão administrativa
  válida e página visível. Guias em segundo plano não criam novos códigos;
  quando a aba voltar a ficar visível, poderão renovar um código expirado.
- A renovação substitui o código temporário no Host e invalida o anterior,
  mas **não** revoga tokens administrativos ou de dispositivos já pareados.
- Falhas de renovação têm intervalo mínimo de 30 s antes de nova tentativa;
  uma requisição pendente impede concorrência. Logout e resposta 401 removem
  o código do DOM e desligam a renovação automática.
- O código permanece somente na memória da aba e não é persistido em
  `localStorage` ou `sessionStorage`.
