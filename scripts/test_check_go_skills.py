"""Tests for the pinned Go skill snapshot checker."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("check-go-skills.py")
spec = importlib.util.spec_from_file_location("check_go_skills", SCRIPT)
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class GoSkillsCheckerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.skills = self.root / "skills"
        self.skills.mkdir()
        skill = self.skills / "golang-example"
        skill.mkdir()
        (skill / "SKILL.md").write_text("pinned Go guidance\n")
        self.license_path = self.skills / "cc-skills-golang.LICENSE"
        self.license_path.write_text("MIT license text\n")
        self.lock_path = self.root / "lock.json"
        self._write_lock()

    def _write_lock(self):
        skill = self.skills / "golang-example"
        lock = {
            "version": 1,
            "repository": "https://github.com/samber/cc-skills-golang",
            "commit": "a" * 40,
            "license": "MIT",
            "license_sha256": checker.hashlib.sha256(self.license_path.read_bytes()).hexdigest(),
            "skills": [{"name": skill.name, "sha256": checker.tree_hash(skill)}],
        }
        self.lock_path.write_text(json.dumps(lock))

    def test_unrelated_ui_skill_directories_are_outside_go_snapshot(self):
        for name in (
            "antislop",
            "antislop-code",
            "antislop-copywriting",
            "antislop-human",
            "antislop-layoutmobile",
            "impeccable",
        ):
            directory = self.skills / name
            directory.mkdir()
            (directory / "SKILL.md").write_text("independent UI guidance\n")

        checker.check_snapshot(self.skills, self.lock_path)

    def test_additional_go_skill_directory_is_rejected(self):
        (self.skills / "golang-unlocked").mkdir()

        with self.assertRaisesRegex(ValueError, "installed Go skill directories differ"):
            checker.check_snapshot(self.skills, self.lock_path)

    def test_skill_content_drift_is_rejected(self):
        (self.skills / "golang-example" / "SKILL.md").write_text("changed guidance\n")

        with self.assertRaisesRegex(ValueError, "skill snapshot drift: golang-example"):
            checker.check_snapshot(self.skills, self.lock_path)

    def test_upstream_license_drift_is_rejected(self):
        self.license_path.write_text("changed license text\n")

        with self.assertRaisesRegex(ValueError, "upstream license differs"):
            checker.check_snapshot(self.skills, self.lock_path)


if __name__ == "__main__":
    unittest.main()
