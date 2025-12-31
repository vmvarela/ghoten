# Ghoten Documentation

This directory contains the documentation website for Ghoten, built with [Docusaurus](https://docusaurus.io/).

## Development

Start the development server:

```bash
cd website
npm install
npm start
```

The site will be available at http://localhost:3000/ghoten/

## Build

Build the static site:

```bash
npm run build
```

## Deployment

The documentation is automatically deployed to GitHub Pages when changes are pushed to the `master` branch via the `.github/workflows/deploy-docs.yml` workflow.

The site will be available at: https://vmvarela.github.io/ghoten/

## Structure

- `docs/` - Documentation content (MDX files)
- `src/` - React components and CSS
- `static/` - Static assets (images, etc.)
- `docusaurus.config.ts` - Docusaurus configuration
- `sidebars.ts` - Sidebar navigation configuration
