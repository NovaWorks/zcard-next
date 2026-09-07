#!/usr/bin/env python3
"""Package a compiled storefront directory for the admin theme installer."""
import argparse
import json
import re
import zipfile
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--dist', type=Path, required=True)
parser.add_argument('--key', required=True)
parser.add_argument('--name', required=True)
parser.add_argument('--version', required=True)
parser.add_argument('--author', default='')
parser.add_argument('--description', default='')
parser.add_argument('--preview', default='', help='Image path relative to dist')
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
if not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,63}', args.key) or args.key == 'classic':
    parser.error('key must be 1-64 lowercase letters/digits/_-, and cannot be classic')
if not args.name.strip() or not args.version.strip():
    parser.error('name and version cannot be empty')
root = args.dist.resolve()
if not (root / 'index.html').is_file():
    parser.error('dist/index.html is missing; build the theme first')
if args.output.resolve().is_relative_to(root):
    parser.error('output ZIP must be outside the dist directory')
if args.preview:
    preview = root / args.preview
    if not preview.resolve().is_relative_to(root) or not preview.is_file():
        parser.error('preview must be an existing image inside dist')
files = sorted(p for p in root.rglob('*') if p.is_file())
if any(p.is_symlink() for p in root.rglob('*')):
    parser.error('dist cannot contain symbolic links')
if sum(p.stat().st_size for p in files) > 100 * 1024 * 1024:
    parser.error('dist exceeds 100MB expanded size')
manifest = dict(schema_version=1, key=args.key, name=args.name, version=args.version,
                author=args.author, desc=args.description, preview=args.preview)
args.output.parent.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(args.output, 'w', zipfile.ZIP_DEFLATED) as archive:
    archive.writestr(f'{args.key}/theme.json', json.dumps(manifest, ensure_ascii=False, indent=2))
    for file in files:
        relative = file.relative_to(root)
        if relative.as_posix() == 'theme.json' or any(p.startswith('.') for p in relative.parts):
            continue
        archive.write(file, f'{args.key}/{relative.as_posix()}')
if args.output.stat().st_size > 20 * 1024 * 1024:
    args.output.unlink()
    parser.error('ZIP exceeds the 20MB upload limit')
print(f'Created {args.output} ({args.output.stat().st_size} bytes)')
