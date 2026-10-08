import os
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def write(path, text):
    with open(path, "w") as f:
        f.write(text)


class Copyright(unittest.TestCase):
    """The copyright audit's verdicts, on a throwaway repository with an owner, a
    guest who left lines behind under a mixed-case address, and a bot."""

    def test_verdicts(self):
        with tempfile.TemporaryDirectory() as d:
            def run(author, *args):
                name, email = author.split("|")
                env = {**os.environ, "GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email, "GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email}
                subprocess.run(["git", "-C", d, "-c", "commit.gpgsign=false", *args], check=True, capture_output=True, env=env)

            def put(name, text):
                write(os.path.join(d, name), text)

            owner, guest, bot = "Owner|o@x.test", "Guest Person|G@x.test", "dependabot[bot]|1+dependabot[bot]@users.noreply.github.com"
            run(owner, "init", "-q")
            put("a.txt", "one\ntwo\nthree\n")
            put("go.mod", "v1\n")
            put("gone.txt", "temporary\n")
            run(owner, "add", ".")
            run(owner, "commit", "-q", "-m", "owner")
            put("b.txt", "guest line\nguest line 2\n")
            put("a.txt", "one\ntwo\nthree\nguest in a\n")
            run(guest, "add", ".")
            run(guest, "commit", "-q", "-m", "guest")
            run(guest, "rm", "-q", "gone.txt")
            run(guest, "commit", "-q", "-m", "guest removes a file")
            put("go.mod", "v2\n")
            run(bot, "add", ".")
            run(bot, "commit", "-q", "-m", "bump")
            out = subprocess.run(["bash", os.path.join(HERE, "copyright.sh"), "o@x.test", d], check=True, capture_output=True, text=True).stdout
        rows = {line.split(" <")[0][2:]: line for line in out.splitlines() if line.startswith("| ") and "<" in line}
        self.assertIn("yes (the owner)", rows["Owner"])
        self.assertIn("a.txt, b.txt | 3 | **needs consent**", rows["Guest Person"])
        self.assertIn("yes (a bot", rows["dependabot[bot]"])


if __name__ == "__main__":
    unittest.main()
