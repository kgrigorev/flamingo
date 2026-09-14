# Vendored: Superpowers skills

The following skill directories under `.claude/skills/` are vendored from the
[Superpowers](https://github.com/obra/superpowers) plugin (not written for
this repo):

- brainstorming
- dispatching-parallel-agents
- executing-plans
- finishing-a-development-branch
- receiving-code-review
- requesting-code-review
- subagent-driven-development
- systematic-debugging
- test-driven-development
- using-git-worktrees
- using-superpowers
- verification-before-completion
- writing-plans
- writing-skills

They are plain copies of `skills/*` from https://github.com/obra/superpowers
at commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797` (v6.3.0), placed directly
under `.claude/skills/` (rather than declared as a marketplace plugin) so they
load in every session, including headless/cloud sessions where the plugin
trust dialog never runs. The plugin's SessionStart hook (which auto-injects
the `using-superpowers` skill as context on every session start) was
intentionally left out — these load like any other project skill instead.

There is no automatic update mechanism: to pick up a newer Superpowers
release, re-clone the pinned version, diff `skills/` against the directories
above, and recopy.

## License

MIT License

Copyright (c) 2025 Jesse Vincent

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
