import io
import json
import os
import tempfile
import unittest

import audit

def write(path, text):
    with open(path, "w") as f:
        f.write(text)


class Classify(unittest.TestCase):
    def test_licenses(self):
        cases = {
            "MIT": "Permission is hereby granted, free of charge, to any person obtaining a copy",
            "BSD-3-Clause": "Redistribution and use in source and binary forms, with or without modification... Neither the name of the copyright holder",
            "BSD-2-Clause": "Redistribution and use in source and binary forms, with or without modification, are permitted",
            "Apache-2.0": "Apache License\n Version 2.0, January 2004",
            "ISC": "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee",
            # The MPL names the GNU licenses as secondary licenses: it must stay the MPL.
            "MPL-2.0": 'Mozilla Public License Version 2.0 ... "Secondary License" means the GNU General Public License, the GNU Lesser General Public License or the GNU Affero General Public License',
            "LGPL": "GNU LESSER GENERAL PUBLIC LICENSE Version 3 ... the GNU General Public License",
            "UNKNOWN": "All rights reserved. Do not copy.",
        }
        for want, text in cases.items():
            self.assertEqual(audit.classify(text), want)

    def test_flagged(self):
        for lic, want in {"MIT": False, "BSD-3-Clause+MIT+Public-Domain": False, "(MIT OR WTFPL)": False,
                          "MPL-2.0": True, "LGPL": True, "CC-BY-SA-4.0": True, "UNKNOWN": True}.items():
            self.assertEqual(audit.flagged(lic), want, lic)


class Npm(unittest.TestCase):
    def test_build_tooling_is_separate_and_a_missing_license_is_unknown(self):
        with tempfile.TemporaryDirectory() as d:
            lock = {"packages": {"": {}, "node_modules/react": {"version": "19.0.0", "license": "MIT"},
                                 "node_modules/a/node_modules/lightningcss": {"version": "1.0.0", "license": "MPL-2.0", "dev": True},
                                 "node_modules/bare": {"version": "1.0.0"}}}
            write(os.path.join(d, "package-lock.json"), json.dumps(lock))
            audit.FRONTEND = d
            got = {x["name"]: x for x in audit.npm_deps()}
        self.assertEqual(sorted(got), ["bare", "lightningcss", "react"])
        self.assertTrue(got["lightningcss"]["dev"] and not got["react"]["dev"])
        self.assertEqual(got["bare"]["license"], "UNKNOWN")


class Notice(unittest.TestCase):
    def test_keeps_license_text_and_names_what_is_missing(self):
        with tempfile.TemporaryDirectory() as d:
            write(os.path.join(d, "LICENSE"), "Permission is hereby granted, free of charge\nCopyright (c) Someone")
            out = io.StringIO()
            missing = audit.notice([
                {"name": "example.test/has", "version": "v1", "license": "MIT", "dir": d, "dev": False},
                {"name": "example.test/none", "version": "v1", "license": "MIT", "dir": "", "dev": False},
                {"name": "example.test/tooling", "version": "v1", "license": "MPL-2.0", "dir": "", "dev": True},
            ], out=out)
        self.assertEqual(missing, ["example.test/none"])  # build tooling is not in NOTICE
        for want in ("example.test/has v1 (MIT)", "Copyright (c) Someone", "internal/jevpick/snapshot.js", "Copyright (c) 2026 Browser Use"):
            self.assertIn(want, out.getvalue())
        self.assertNotIn("example.test/tooling", out.getvalue())


if __name__ == "__main__":
    unittest.main()
