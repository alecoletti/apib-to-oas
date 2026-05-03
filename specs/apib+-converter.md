# Blueprint+ converter contract

This document specifies how a converter reading Blueprint+
(see [`apib+.md`](apib%2B.md)) produces OpenAPI 3.x: schema
precedence rules, diagnostic codes, conformance modes, sidecar
configuration, and the conversion pipeline.

---

## 1. Conversion pipeline

```
.apib  →  Drafter (API Elements / Refract JSON)
       →  Blueprint+ post-processor (pure, no I/O)
       →  OpenAPI 3.x typed model
       →  YAML or JSON output
```

The post-processor is deterministic and side-effect-free: same
input, same output, every time.

---

## 2. Conformance modes

| Mode                       | Unknown metadata             | Unrecognised syntax |
|----------------------------|------------------------------|---------------------|
| **Permissive** *(default)* | preserved as `x-*` + warning | dropped + warning   |
| **Strict**                 | error                        | error               |

`--strict` on the converter CLI promotes every warning to an error.

---

## 3. Schema precedence

Inside a request or response, schemas can come from several places.
Highest priority first; a higher source replaces a lower one
without merging:

1. Inline `+ Attributes` on the request / response.
2. Raw `+ Schema` block (fallback only — see §3.1).
3. Action-level `+ Attributes` (sibling of the `+ Request`s) —
   default for every Request that doesn't override.
4. Resource-level `+ Attributes` (sibling of the actions) —
   default for every 2xx Response that declares a Content-Type.
5. Inferred from `+ Body` JSON example — minimal
   `{type, properties}` so docs renderers display the payload.

### 3.1 Why `+ Schema` is fallback only

Drafter automatically emits a stripped-down JSON Schema asset (no
descriptions, examples, formats, no `$ref`s) for every
`+ Attributes` block. Picking `+ Schema` over `+ Attributes` would
silently throw away every per-member description the author wrote.
Hand-written `+ Schema` blocks are honoured only when no
`+ Attributes` is present.

---

## 4. Multiple examples per (status, content-type)

Multiple `+ Request <Title>` / `+ Response <Status> (<Title>)`
blocks sharing the same `(status, content-type)` are emitted as an
OAS `examples:` map keyed by Title. Without titles, names are
auto-generated as `example1`, `example2`, …. Identical example
values are deduplicated.

---

## 5. Cross-resource scoping

If two resources declare the same base path (`/x{?q}` and `/x`),
the `q` query parameter belongs only to operations under the
resource that declared it. Path parameters are still shared (they
are intrinsic to the URL).

---

## 6. Security

Security schemes can come from three sources. They merge across
sources; the sidecar wins on collision because it is applied last.

1. **Schemes** — declared in-source via the reserved
   `## SecuritySchemes (object)` MSON named-type, or via the JSON
   sidecar (see §7).
2. **Document default** — the `SECURITY:` document metadata.
3. **Per-operation override** — `+ Meta + Security:` on the
   action.

### 6.1 Override precedence (highest first)

```
operation `+ Meta + Security:`
  > sidecar overrides.byOperationId
  > sidecar overrides.byPath
  > sidecar overrides.byTag
  > sidecar defaultSecurity
  > APIB document `SECURITY:` metadata
```

An empty `Security:` value (`Security:` with nothing after the
colon) emits `security: []` — auth disabled for that operation.

---

## 7. Security sidecar (JSON)

The same data the `## SecuritySchemes (object)` MSON type
expresses can come from a JSON file passed through
`--security-config`. Useful when authentication is environment-
specific or kept out of source.

```json
{
  "securitySchemes": {
    "BearerAuth": { "type": "http",   "scheme": "bearer", "bearerFormat": "JWT" },
    "ApiKeyAuth": { "type": "apiKey", "in": "header",     "name": "X-API-Key"   }
  },
  "defaultSecurity": [ { "BearerAuth": [] } ],
  "overrides": {
    "byTag":         { "Public": [] },
    "byPath":        { "/healthz": [] },
    "byOperationId": { "loginUser": [ { "BearerAuth": [] } ] }
  }
}
```

---

## 8. Diagnostics

Every diagnostic carries `severity ∈ {error, warning, note}`, a
stable code, and a source location (`line`, `column`).

### 8.1 Errors

| Code | Meaning                                                              |
|------|----------------------------------------------------------------------|
| E001 | Invalid HTTP method.                                                 |
| E002 | Malformed URI template.                                              |
| E003 | Malformed location prefix (e.g. unterminated bracket in `[header:`). |
| E004 | Unknown location prefix (recognised: `header`, `cookie`).            |
| E005 | Duplicate `OperationId` (Strict mode only; W005 otherwise).          |
| E006 | Reference to undefined named type.                                   |
| E007 | Security scheme missing a required field for its declared `type`.    |
| E008 | Unknown security scheme `type` (or unknown OAuth2 flow name).        |

### 8.2 Warnings

| Code | Meaning                                                                   |
|------|---------------------------------------------------------------------------|
| W001 | Unknown metadata key (preserved as `x-*`).                                |
| W002 | Multiple `VERSION:` entries (last wins).                                  |
| W003 | Webhook group declared but target is OAS 3.0 (ignored).                   |
| W005 | Implicit default applied (e.g. duplicate OperationId in Permissive mode). |
| W006 | Deprecated syntax (`# METHOD /path` shorthand).                           |
| W007 | Security scheme name referenced but never declared.                       |

W001 only fires for keys that look like a typo of a recognised one
(bare PascalCase, e.g. `Wibble`). Keys that are obviously
intentional extensions (`-` / `_` / `.` separators or CamelCase
boundaries) are absorbed silently.

---

## Appendix — Mapping table

Compact source → target reference. See [`apib+.md`](apib%2B.md) for
the source-side syntax.

| Source construct                     | OpenAPI JSON Pointer                                  | Cardinality   |
|--------------------------------------|-------------------------------------------------------|---------------|
| `# <API title>`                      | `/info/title`                                         | 1             |
| `VERSION:` (or aliases)              | `/info/version`                                       | 1             |
| `SUMMARY:`                           | `/info/summary`                                       | 0..1          |
| `LICENSE:`                           | `/info/license/name`                                  | 0..1          |
| `LICENSE-ID:`                        | `/info/license/identifier`                            | 0..1          |
| `LICENSE-URL:`                       | `/info/license/url`                                   | 0..1          |
| Top-level `copy` blocks              | `/info/description`                                   | 0..1          |
| `HOST:` / `SERVER:`                  | `/servers/{i}/url`, `/servers/{i}/description`        | 0..n          |
| `WEBHOOK_GROUPS:`                    | routing flag                                          | —             |
| `SECURITY:` (document)               | `/security`                                           | 0..1          |
| `# Group <name>`                     | `/tags/{i}/name`                                      | 0..n          |
| `# Group <name>` prose               | `/tags/{i}/description`                               | 0..1          |
| `## <Title> [<URI>]`                 | `/paths/<URI>` (or `/webhooks/<group>`)               | 0..n          |
| `### <Title> [<METHOD>]`             | `/paths/<URI>/<method>`                               | per resource  |
| `+ Meta → OperationId`               | `/paths/<URI>/<method>/operationId`                   | 0..1          |
| `+ Meta → Tags`                      | `/paths/<URI>/<method>/tags`                          | 0..1          |
| `+ Meta → Deprecated`                | `/paths/<URI>/<method>/deprecated`                    | 0..1          |
| `+ Meta → Docs`                      | `/paths/<URI>/<method>/externalDocs/url`              | 0..1          |
| `+ Meta → Security`                  | `/paths/<URI>/<method>/security`                      | 0..1          |
| `+ Meta → Kind` (group scope)        | `/tags/{i}/kind`                                      | 0..1 (3.2+)   |
| `+ Parameters` (path)                | `/paths/<URI>/parameters/{i}`                         | 0..n          |
| `+ Parameters` (query/header/cookie) | `/paths/<URI>/<method>/parameters/{i}`                | 0..n          |
| `+ Request (<ct>)`                   | `/paths/.../requestBody/content/<ct>`                 | 0..1          |
| `+ Request <Title>`                  | `…/examples/<Title>`                                  | 0..n          |
| `+ Response <code> (<ct>)`           | `/paths/.../responses/<code>/content/<ct>`            | 0..n          |
| `+ Response <code> - <title>`        | `/paths/.../responses/<code>/{summary\|description}`  | 0..1          |
| `+ Headers` (response)               | `/paths/.../responses/<code>/headers/<name>`          | 0..n          |
| `+ Attributes (<Type>)`              | `…/schema` (inlined or `$ref`)                        | precedence §3 |
| `+ Schema` (raw JSON Schema)         | `…/schema`                                            | fallback only |
| `# Data Structures`                  | `/components/schemas/<Name>`                          | 0..n          |
| `## SecuritySchemes (object)`        | `/components/securitySchemes/<MemberName>`            | 0..n          |
| Sidecar JSON                         | same as above (sidecar wins on collision)             | —             |
| `<Foo-Bar>: <value>` (unknown)       | `…/x-foo-bar` (target depends on scope)               | 0..n          |

