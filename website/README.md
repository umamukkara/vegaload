# VegaLoad docs site

The docs live in `docs/`. This folder is a Docusaurus site. A production build writes `build/`.

## Run

```
cd website && npm ci && npm start
```

`npm start` shows draft pages, so the full navigation is visible while you work.

## Build

```
cd website && npm ci && npm run build
```

The production build leaves out any page with `draft: true`.

## Add a page

Add a Markdown file under `docs/`. Set front matter `title` and `description`.

A folder needs `_category_.json` with `label` and `position`. A page at the top of `docs/` sets `sidebar_position` instead.

`draft: true` keeps that page out of the production build. `npm start` still shows it.

## DOCS_URL and DOCS_BASE_URL

`DOCS_URL` is the site origin. It defaults to `https://vegaload.github.io`.

`DOCS_BASE_URL` is the path prefix. It defaults to `/vegaload/`.

Set both in the environment before `npm run build`.

`DOCS_NO_INDEX` defaults to on. Every page then has `<meta name="robots" content="noindex, nofollow">`. Set it to `false` to remove that tag.

## Launch

To go live, set `DOCS_NO_INDEX` to `false`, replace `robots.txt` with one that allows crawlers and lists the sitemap, set `DOCS_URL` to `https://docs.vegaload.ai` and `DOCS_BASE_URL` to `/`, add `website/static/CNAME` with `docs.vegaload.ai`, and set the custom domain in the repo's Pages settings.
