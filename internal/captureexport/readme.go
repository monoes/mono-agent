package captureexport

// readmeText ships inside every archive. An archive that needs the tool
// that made it in order to be understood is not portable, whatever its file
// extension says.
const readmeText = `monoagent capture archive
=========================

This is a plain gzipped tar file. Nothing in it is proprietary, encrypted or
packed: if monoagent is not available, everything below still works.

  tar -tzf <this file>            # list what is inside
  tar -xzf <this file>            # unpack into ./manifest.json and ./captures/
  tar -xzOf <this file> manifest.json | less    # read the index without unpacking

Layout
------

  manifest.json          index of every capture in this archive (see below)
  README.txt             this file
  captures/<name>/       one directory per captured page
  skipped.json           only if something was left out, and what (see below)

Each captures/<name>/ directory is one web page, saved the moment it was
read, and holds some of:

  meta.json              where the page came from (always present)
  page.mhtml             the page as the browser saw it — open it in Chrome
  page.html              the HTML the server sent, for a crawled page
  page.pdf               a printed copy
  readable.md            the article as Markdown — this is the text to read
  screenshot.png         a full-page picture

meta.json
---------

  url            the address that was visited
  canonicalUrl   the address the page says is its real one
  title          the page title
  byline         the author, when the page declared one
  publishedAt    when the page says it was published
  capturedAt     when this copy was taken (ISO-8601, UTC)
  httpStatus     the HTTP status of the capture
  contentHash    sha256 of the capture's text, for spotting re-captures
  tags           tags added at save time
  collection     the collection it was filed under
  source         "extension" (saved from a browser), "crawl", or "monobrowse"

Checking it
-----------

manifest.json lists every file with its size and sha256. After unpacking:

  sha256sum captures/<name>/readable.md

should match the "sha256" field for that file in manifest.json.

If skipped.json is present, it lists what this archive does NOT hold and
why. Captures are exported from a live directory — one can be deleted while
the archive is being written — so manifest.json may name a file that was
gone before it could be read. skipped.json is where that is recorded.

Putting it back
---------------

  monoagentcli capture import <this file>

or simply unpack it and move the captures/<name>/ directories into your
inbox (~/.monomind/inbox/). The directories are the format; there is no
database to rebuild.
`
