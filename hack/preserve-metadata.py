#!/usr/bin/env python3
"""Stop the CRD from pruning nested metadata.

controller-gen renders the ObjectMeta embedded in JobTemplateSpec and PodTemplateSpec as a
bare `type: object` with no properties. Under a structural schema the API server prunes
everything inside such a node, so labels and annotations a user writes on jobTemplate or on
the pod template are silently dropped before the controller ever sees them. Marking just
those two nodes as preserve-unknown-fields keeps them, and leaves pruning — and therefore
typo detection — intact everywhere else.

Usage: preserve-metadata.py <crd.yaml>
"""

import re
import sys

MARKER = "Standard object's metadata"
PRESERVE = "x-kubernetes-preserve-unknown-fields: true"


def patch(text: str) -> tuple[str, int]:
    out: list[str] = []
    seen_marker = False
    patched = 0

    for line in text.splitlines():
        if MARKER in line:
            seen_marker = True
        match = re.fullmatch(r"(\s*)type: object", line)
        if seen_marker and match:
            out.append(line)
            out.append(f"{match.group(1)}{PRESERVE}")
            seen_marker = False
            patched += 1
            continue
        out.append(line)

    return "\n".join(out) + "\n", patched


def main() -> int:
    path = sys.argv[1]
    with open(path, encoding="utf-8") as handle:
        text = handle.read()

    if PRESERVE in text:
        return 0

    patched_text, count = patch(text)
    if count == 0:
        print(f"{path}: no nested metadata schemas found", file=sys.stderr)
        return 1

    with open(path, "w", encoding="utf-8") as handle:
        handle.write(patched_text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
