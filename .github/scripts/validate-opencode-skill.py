"""Check the independently installable OpenCode caller skill."""

from pathlib import Path
import re

root = Path(".opencode/skills/revmux")
text = (root / "SKILL.md").read_text()
assert text.startswith("---\nname: revmux\n"), "missing skill identity"
assert "\ndescription:" in text, "skill must be discoverable"
for relative in set(re.findall(r"(?:scripts|references)/[\w.-]+\.(?:sh|py|md)", text)):
    assert (root / relative).is_file(), f"missing skill file: {relative}"
assert not (root / "agents/openai.yaml").exists(), "Codex descriptor in OpenCode skill"
print("OpenCode skill payload is valid")
