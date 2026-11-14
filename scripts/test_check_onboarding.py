import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("check_onboarding", Path(__file__).with_name("check-onboarding.py"))
checker = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(checker)


class OnboardingChecksTest(unittest.TestCase):
    def test_canonical_onboarding_guides_and_dashboard_readme_are_checked(self):
        self.assertTrue(
            {
                "docs/local-development.md",
                "docs/setup.md",
                "docs/tooling/development-tools.md",
                "web/dashboard/README.md",
            }.issubset(checker.PAGES)
        )

    def test_setup_quickstart_is_never_a_run_example(self):
        self.assertNotIn("docs/setup.md", checker.EXAMPLES)
        self.assertEqual(
            checker.EXAMPLES,
            {
                "README.md": "First safe command",
                "docs/development-guide.md": "1. Start without infrastructure",
            },
        )

    def test_setup_tool_versions_match_repository_pins(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs").mkdir()
            (root / "web/dashboard").mkdir(parents=True)
            (root / "go.mod").write_text("module hello\n\ngo 1.26.11\n")
            (root / "web/dashboard/package.json").write_text('{"packageManager":"pnpm@11.25.0"}')
            setup = root / "docs/setup.md"
            setup.write_text("| Go | `1.26.11` |\n| pnpm | `11.25.0` |\n")
            checker.check_pinned_tool_versions(root)
            setup.write_text("| Go | `1.26.10` |\n| pnpm | `11.25.0` |\n")
            with self.assertRaisesRegex(ValueError, "Go version must match"):
                checker.check_pinned_tool_versions(root)
            setup.write_text("| Go | `1.26.11` |\n| pnpm | `11.24.0` |\n")
            with self.assertRaisesRegex(ValueError, "pnpm version must match"):
                checker.check_pinned_tool_versions(root)

    def test_numbered_and_duplicate_headings(self):
        self.assertEqual(checker.heading_ids("## 2. Run locally\n## Same\n## Same\n"), {"2-run-locally", "same", "same-1"})

    def test_missing_link_and_anchor_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            page = root / "README.md"
            page.write_text("[missing](absent.md)\n")
            with self.assertRaisesRegex(ValueError, "missing link target"):
                checker.check_page(root, "README.md")
            (root / "guide.md").write_text("## Present\n")
            page.write_text("[wrong heading](guide.md#absent)\n")
            with self.assertRaisesRegex(ValueError, "missing heading"):
                checker.check_page(root, "README.md")
            page.write_text("[valid](guide.md#present)\n")
            checker.check_page(root, "README.md")

    def test_broken_bash_and_unclosed_fences_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            page = root / "README.md"
            page.write_text("```bash\nif\n```\n")
            with self.assertRaisesRegex(ValueError, "invalid Bash"):
                checker.check_page(root, "README.md")
            page.write_text("```bash\ngo test\n")
            with self.assertRaisesRegex(ValueError, "unclosed code fence"):
                checker.check_page(root, "README.md")

    def test_indented_bash_fences_are_checked_and_dedented(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            page = root / "README.md"
            page.write_text("- Example:\n  ```bash\n  if\n  ```\n")
            with self.assertRaisesRegex(ValueError, "invalid Bash"):
                checker.check_page(root, "README.md")

            page.write_text(
                "- Example:\n"
                "  ```bash\n"
                "  if true; then\n"
                "    cat <<'EOF'\n"
                "    heredoc text with `fence-looking` content\n"
                "EOF\n"
                "  fi\n"
                "  ```\n"
            )
            checker.check_page(root, "README.md")

    def test_only_first_safe_section_block_is_selected(self):
        text = "## Safe\n```bash\ngo build -mod=readonly ./...\n```\n```bash\ndocker compose up\n```\n## Other\n"
        self.assertEqual(checker.example_commands(text, "Safe"), [["go", "build", "-mod=readonly", "./..."]])
        with self.assertRaisesRegex(ValueError, "missing example section"):
            checker.example_commands(text, "Missing")

    def test_side_effecting_commands_cannot_enter_run_mode(self):
        for command in (
            ["bash", "scripts/integration.sh"],
            ["docker", "compose", "up"],
            ["npm", "install"],
            ["go", "install", "example/tool"],
            ["go", "run", "-mod=readonly", "./cmd/server"],
            ["go", "test", "-mod=readonly", "./tests/integration/..."],
            ["go", "test", "./internal/jsonobject/..."],
            ["go", "test", "-mod=readonly", "./internal/jsonobject/...", "&&", "docker"],
            ["go", "test", "-mod=readonly", "./internal/jsonobject/...", "-exec", "unsafe"],
            ["go", "build", "-mod=readonly", "-o", "existing-file", "./..."],
        ):
            with self.subTest(command=command), self.assertRaises(ValueError):
                checker.validate_command(command)

    def test_examples_fail_for_no_tests_or_nonzero_exit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "README.md").write_text("## Safe\n```bash\ngo test -mod=readonly ./internal/jsonobject/...\n```\n")
            for result in (
                subprocess.CompletedProcess([], 1, "", "compile error"),
                subprocess.CompletedProcess([], 0, "ok package [no tests to run]\n", ""),
            ):
                with patch.object(checker, "EXAMPLES", {"README.md": "Safe"}), patch.object(checker.shutil, "which", return_value="go"), patch.object(checker.subprocess, "run", return_value=result), patch("builtins.print"), self.assertRaises(ValueError):
                    checker.run_examples(root)


if __name__ == "__main__":
    unittest.main()
