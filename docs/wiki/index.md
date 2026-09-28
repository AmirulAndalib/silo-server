---
title: Silo Wiki
description: Legacy operator pages that are moving to the user manual on siloserver.org.
summary: Index of digestible Silo docs by audience and subject.
tags:
  - silo
  - docs
  - wiki
audience:
  - end-user
  - operator
last_reviewed: 2026-08-20
related: []
---

# Silo Wiki

> [!IMPORTANT]
> This directory takes no new pages or sections. Guides for installing, configuring, and running
> Silo live in the [user manual](https://siloserver.org/docs), whose source is
> [Silo-Server/siloserver.org](https://github.com/Silo-Server/siloserver.org). Send new operator
> and user guides there. The pages below remain until they move to the manual; correct one only
> when a code change makes it wrong.

## Sections

## Getting Started

- No pages yet.

## Features

- No pages yet.

## Admin

- [Supported Media Folder Structures and Naming](admin/media-folder-and-naming.md) - Accurate
  reference for the folder layouts and filenames Silo can scan and match today.
- [Collection Templates](admin/collection-templates.md) - Curated, one-click starting points for
  synced library collections sourced from TMDB, Trakt, and MDBList.
- [Local NFO Metadata](admin/nfo-local-metadata.md) - Supported NFO sidecar fields, how they merge
  with online providers, and the naming-supplies-structure contract.
- [Monitoring Stream Nodes](admin/monitoring-nodes.md) - Reading the Nodes page columns, re-probing
  a node's GPU after a driver change, scratch-disk admission, and scraping node metrics.

## Deployment

- [Deploy Silo with Docker](deployment/docker.md) - Install and operate Silo with Docker Compose,
  including storage, GPU acceleration, search, distributed roles, tuning, backups, and updates.

## Troubleshooting

- No pages yet.

## Editing Rules

- Do not add pages or sections here. New operator and user guides go to the user manual.
- Correct an existing page when a code change makes it wrong, and open a
  [siloserver.org](https://github.com/Silo-Server/siloserver.org) issue if the manual needs the
  same fix.
- Keep architecture material in `docs/architecture/`.
- Use YAML frontmatter, portable Markdown, and `## Source References` sections instead of copied
  code.
