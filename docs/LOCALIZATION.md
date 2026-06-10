# Localization (i18n)

> Goal: serve the web UI in English and Spanish, with the architecture ported
> from **b2b-fleet-mgr-app** and adapted to fleet-lite idioms.

Created: 2026-06-09
Status: shipped (PR #27) — English + Spanish

---

## How it works

Runtime localization via [`@lit/localize`](https://lit.dev/docs/localization/overview/)
(runtime mode). Source locale is `en`; the only target locale is `es`.

| Concern | Where |
| --- | --- |
| Config | `web/lit-localize.json` (sourceLocale `en`, targetLocales `["es"]`, xliff interchange) |
| Runtime module | `web/src/localization.ts` — `configureLocalization`, exports `getLocale`/`setLocale` |
| Translations (source of truth) | `web/xliff/es.xlf` — hand-translated `<target>`s |
| Generated bundle | `web/src/generated/locale-codes.ts`, `web/src/generated/locales/es.ts` |
| Persistence | `PrefsService` (`web/src/services/prefs-service.ts`) — `getLocale`/`setLocale`/`toggleLocale`, `fleet-lite:locale` key + `navigator.language` fallback |
| Bootstrap | `app-root.connectedCallback` — `await setLocale('es')` before first paint |
| Switch UI | Account Settings "Language" row (`web/src/views/account-settings.ts`) — toggles `en`↔`es`, then `window.location.reload()` |

The language switch reloads the page (b2b's approach) rather than using
`@localized()` reactive decorators — simpler, and avoids touching every
component. The units toggle stays live; only language changes reload.

## Adding/changing strings

1. Wrap user-facing text in components with `msg('Text')`, `msg(str\`…${x}…\`)`
   (interpolation), or `msg(html\`…\`)` (markup).
2. `cd web && npm run localize:extract` → updates `xliff/es.xlf` with new units.
3. Fill in the Spanish `<target>` for each new `<trans-unit>`.
4. `npm run localize:build` → regenerates `src/generated/locales/es.ts`.
5. Commit the changed components **and** the regenerated `src/generated/*` +
   `xliff/es.xlf` (they are intentionally tracked — see below).

## Conventions / gotchas

- **Generated files are committed.** `src/generated/` and `xliff/` were removed
  from `web/.gitignore`. The `build` script is `tsc && vite build` (it does *not*
  run `localize:build`), so a fresh clone/CI would fail to compile without the
  committed `locale-codes.ts`. `xliff/es.xlf` is also the version-controlled
  source of truth for translations.
- **Never call `msg()` at module scope.** A `msg()` in a module-level `const`
  evaluates at import time — before `setLocale()` runs — and freezes the source
  locale. Use a render-time thunk instead. Reference: `side-nav.ts` `ITEMS`
  (`label: () => msg(...)`) and `glovebox.ts` `MISSING_BLURBS`.
- **Avoid a raw `&` in translated strings.** lit-localize emits XLIFF `&amp;`
  into the generated string literal verbatim, so it renders as a literal
  `&amp;`. Prefer idiomatic Spanish "y" (e.g. "Privacidad y Cuenta"). English
  source strings are unaffected (raw JS literals).
- Proper nouns left untranslated: DIMO, VIN, DTCs, IMEI, Token, Passkey,
  API key/Client ID, SACD, `0x…`, hex, URLs.

## Adding another locale (e.g. `fr`)

1. Add `"fr"` to `targetLocales` in `web/lit-localize.json`.
2. `npm run localize:extract` → creates `xliff/fr.xlf`; translate its targets.
3. `npm run localize:build`.
4. Add the option to the Account Settings "Language" switcher and to
   `PrefsService` `Locale` / `localeLabel`. With >2 locales, replace the
   toggle with a real picker (the current `toggleLocale` is `en`↔`es` only).

## Deferred follow-ups (tracked, not done)

These were intentionally left out of the initial localization PR (#27):

1. **Localize the Measurement Units preference value.** The Account Settings
   "Measurement Units" trailing label still renders "METRIC" / "IMPERIAL" in
   English even on the Spanish UI, because `unitsLabel()`
   (`web/src/utils/units.ts`) returns untranslated strings. It belongs to the
   units feature, not the language set. Fix: wrap the two return values in
   `msg()` and re-extract/translate/build.
2. **Localize dynamically-composed title strings.** Strings built from API data
   — `Token #${id}`, vehicle titles assembled from year/make/model — were left
   unwrapped as data. If these phrasings ("Token", fallback "Vehicle #…")
   should be translated, wrap the static fragments with `msg(str\`…\`)`.
