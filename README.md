# cairnfield

cairnfield is a lightweight, self-hosted notes app meant to cover the simple
parts of an Obsidian-style workflow without needing a desktop sync setup. It is
built for markdown notes, folders, attachments, search, and private hosting.

![cairnfield screenshot](screenshot.png)

## Features

- Markdown note editing with folders
- Full-text search
- Attached files and images
- PGP-encrypted notes and encrypted attachments
- Note sharing between users on the same instance
- Templates for daily notes and other repeatable note types
- Zip import and export backups
- Optional OIDC login for existing accounts

## Run With Docker

```sh
docker run -d \
  --name cairnfield \
  -p 8080:8080 \
  -v cairnfield-data:/data \
  ghcr.io/grahamsz/cairnfield:latest
```

Then open `http://localhost:8080` and create the first admin account.

## License

cairnfield is licensed under the GNU Affero General Public License v3.0 only.
See [LICENSE](LICENSE).
