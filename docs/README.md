# Beacons documentation

[Docusaurus](https://docusaurus.io/) site for Beacons. Its own Node package, separate from the Go project.

## Local development

```sh
pnpm install    # once
pnpm start      # live preview at http://localhost:3000/beacons/
pnpm build      # production build into ./build
pnpm serve      # serve the production build locally
```

## Deployment

`.github/workflows/docs.yml` builds and deploys to GitHub Pages on pushes to `main` that touch `docs/`. A single current version is published; Docusaurus has built-in [versioning](https://docusaurus.io/docs/versioning) we can enable later if needed.
