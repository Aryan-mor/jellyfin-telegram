# Auto-add filter

The filter decides which titles from TMDB trending are added to the library automatically. It is
edited in the panel (Sources, Auto-add filter) and in the setup wizard (Filter step, skippable).

## Scope

- Only automatically added titles (TMDB trending) are filtered.
- Seerr requests are never filtered. They are always searched and added.
- Direct files and News channels are not filtered either. The title is not identified before the
  file is copied, so there is nothing to match against.

## Rules

Each part (genres, countries, languages, networks) has an include list and an exclude list:

| Include | Exclude | Result |
| --- | --- | --- |
| set | empty | only those values pass |
| empty | set | everything except those values passes |
| set | set | the include list passes, the exclude list is rejected (a value cannot be in both) |
| empty | empty | the part has no say |

- Parts are joined with OR. A title is added when any part that has a rule accepts it. Example:
  exclude one country but include one network, and a series from that country on that network is
  still added.
- When no part has any value, only titles with one of the default genres pass.
- Adult titles are blocked unless `allow_adult` is on, whatever the other rules say.

## Routes

Both pairs are behind the page's code guard (`?code=...`) and use the same handler.

- `GET /panel/auto-filter` and `GET /setup/auto-filter`
- `POST /panel/auto-filter` and `POST /setup/auto-filter`

### GET response

```json
{
  "filter": { "allow_adult": false, "genres": { "include": [], "exclude": [] }, "countries": { "include": [], "exclude": [] }, "languages": { "include": [], "exclude": [] }, "networks": { "include": [], "exclude": [] } },
  "configured": false,
  "default_genres": ["Action", "Adventure"],
  "options": {
    "genres": [{ "id": "Drama", "name": "Drama" }],
    "genres_source": "tmdb",
    "countries": [{ "id": "US", "name": "United States" }],
    "languages": [{ "id": "en", "name": "English" }],
    "networks": [{ "id": "netflix", "name": "Netflix" }]
  }
}
```

- `configured` is false while no part has a value. `default_genres` then decide.
- `genres_source` is `tmdb` when the genre list came from TMDB, otherwise `default`.
- Countries are ISO 3166-1 codes, languages ISO 639-1 codes, networks ids such as `netflix`. Names
  are in English.

### POST

Body: a `filter` object as above. Response: `{ "ok": true, "filter": Filter, "configured": bool }`,
or `{ "error": string }` with a 4xx/5xx status. Unknown country, language or network values, and a
value in both lists of one part, are rejected.

## Config keys

Saved to the settings file (`.env`) as comma separated lists. A key in the file wins over the
environment, so a change from the panel applies without a restart.

| Key | Meaning |
| --- | --- |
| `AUTO_FILTER_ALLOW_ADULT` | `true` allows adult titles |
| `AUTO_FILTER_GENRES` / `AUTO_FILTER_EXCLUDE_GENRES` | genre names (English, TMDB) |
| `AUTO_FILTER_COUNTRIES` / `AUTO_FILTER_EXCLUDE_COUNTRIES` | origin country codes |
| `AUTO_FILTER_LANGUAGES` / `AUTO_FILTER_EXCLUDE_LANGUAGES` | original language codes |
| `AUTO_FILTER_NETWORKS` / `AUTO_FILTER_EXCLUDE_NETWORKS` | `netflix`, `prime_video`, `disney_plus`, `apple_tv_plus`, `hbo_max`, `hulu`, `paramount_plus` |

The older `TRENDING_ALLOW_ADULT`, `TRENDING_GENRES` and `TRENDING_EXCLUDE_GENRES` are read when the
matching `AUTO_FILTER_*` key is missing.
