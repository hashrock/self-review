---
name: self-review
description: Explain an existing code change as a guide that walks through it in a meaningful order with focused inline diffs, and open it in the local self-review window.
argument-hint: "[commits]"
disable-model-invocation: true
---

# Write a change guide

Write a guide to the code change relevant to this conversation, so that a
reader who did not take part in it can build a clear mental model of the
change. This is a request to explain, not to modify code.

Arguments: `$ARGUMENTS`

## Reader's language

Run `self-review config lang` first. It prints the reader's language
(for example `ja`). Write all of the guide's prose in that language: the
title, overview, section headings, excerpt introductions, related-location
labels and the recap. Keep code, identifiers and paths as they are. These
instructions are in English; the guide is not.

## Scope

- No argument (whole branch): one guide for the diff against the base branch,
  that is, the whole pull request. Save it to `.self-review/guide.md` with
  `comparison="branch"`.
- `commits` (by commit): one guide per commit.
  - Run `self-review commits` to list the commits and which have guides.
  - Write guides only for commits marked `no-guide`, oldest first, unless the
    user asks to rewrite one.
  - Save each to `.self-review/commits/<full hash>.md` with
    `comparison="commit" commit="<full hash>"`. Explain that commit's own diff.
  - If the list ends with `worktree` (uncommitted changes), write
    `.self-review/commits/worktree.md` with `comparison="uncommitted"`.
  - Keep each guide to its commit. Mention neighbouring commits only when it
    helps the reader see how they connect.

## Steps

1. Choose the guides to write and their comparison from "Scope" above.
2. From the conversation, pick up the motivation, the author's wording, and
   decisions made along the way. For commits, use the commit message too.
3. Read the whole change and the code around it: callers and tests.
   For one commit, `git show <hash>` shows its diff.
4. Work out the final behavior, the main control and data paths,
   responsibility boundaries, side effects and invariants.
5. Plan a short route that explains the change to someone new to it. Follow
   understanding, not file or diff order: by default from an entry point
   inwards, or start from a failing test or a data structure when that is
   clearer.
6. Write and save the guides. If `.self-review/` is not ignored by Git, add it
   to `.git/info/exclude`.
7. Run `self-review check` (it checks every guide in `.self-review/`) and fix
   the problems. Fix length warnings too, unless the excerpt cannot get smaller.
8. Run `self-review open` to show the user. The window opens in its own
   process and the command returns at once; an open window reloads. For
   commit guides, add `--view commit:<full hash>` to start at the first one,
   and tell the user they can step through commits at the top of the window.

## How to write

Favor continuity over exhaustive narration. Short guides get read.

- Write in a friendly, plain tone for a capable programmer: precise, without
  compressed shorthand or over-explaining basics.
- Keep the author's terms where they still fit. Explain a project-specific
  concept briefly where it is first needed, not before.
- Overview: state the author's problem, desired outcomes and constraints in
  their own terms, briefly. Use a short list when there are several outcomes.
  Describe user-visible behavior in concrete terms; leave implementation
  abstractions for the sections. End with one sentence on the order of the
  sections. Do not list files or implementation details in the overview.
- Give each section a conceptual heading and one story.
- Excerpts (`<excerpt>`) show the smallest changed range that reveals the
  behavior.
  - The introduction inside says what it shows and why it is the next stop.
    When the link to what the reader saw before is not obvious, say it.
  - Add prose after an excerpt only for flow the diff does not show by itself.
  - Excerpt unchanged code only when it is needed context.
- Code that needs no diff (supporting functions, types, tests, changes a
  sentence summarizes) gets a related location (`<related-location/>`), not an
  excerpt. Do not point to the same place twice or to trivial details.
- Excerpt a test only when it best explains the failure, the intended use or
  an invariant. Summarize similar tests in a sentence.
- Do not narrate syntax line by line. Summarize mechanical changes.
- Mention meaningful changes left out of the excerpts, and why, in a phrase.
- Tell new behavior from preserved behavior. State preconditions and
  fallbacks where they matter. Remove claims the repository does not support.
- Before finishing, check that every excerpt range contains the change its
  introduction describes.

### Length

- An excerpt shows at most 30 lines, including 3 lines of context on each
  side. `self-review check` warns about longer ones: narrow the range, split
  it, or use a related location. A new file is all changes, so an excerpt of
  it without a line range shows the whole file.
- Introductions are one or two sentences.
- At most five overview bullets; three to six sections.
- The recap is a few sentences on tests, limitations and deferred work.

## Format

Save guides in this format. The example shows syntax only (and is in English;
write yours in the reader's language).

```
<change-guide title="Make the P shift lock a setting" comparison="branch">
# Make the P shift lock a setting

Overview paragraph.

## Settings in the physics engine

Section introduction.

<excerpt path="public/js/physics.js" start-line="5" end-line="8">
The new `settings` object.
</excerpt>

Prose after the excerpt, when needed.

<related-location label="Car.requestGear" path="public/js/physics.js" start-line="120" end-line="140"/>

## Recap

How the pieces connect, tests, limitations and deferred work, briefly.
</change-guide>
```

### Rules

- Exactly one `<change-guide>` per file.
  - `title`: a short name of the change's outcome, in the reader's language.
  - `comparison`: `branch`, `uncommitted`, `direct` or `commit`. Required.
  - `base`: for `branch`, the base branch; the default branch when omitted.
  - `previous-version` / `next-version`: only for `direct`, as full commit
    hashes. Without `next-version` it compares with the working tree.
  - `commit`: only for `commit`, as a full hash. Compares with its parent.
- The body has exactly one `#` heading, one or more `##` sections, and ends
  with a recap section: `## Recap`, or its translation in the reader's
  language (`## まとめ` in Japanese).
- `<excerpt path="..." start-line="..." end-line="...">introduction</excerpt>`
  - `path` is a worktree-relative Unix path.
  - Lines start at 1. Use new-version line numbers for additions and
    replacements, old-version numbers for pure deletions. For a commit guide,
    they are the numbers in the files at that commit.
  - Without line numbers it shows every change in the file.
  - A range with no changes shows that code unchanged (for needed context).
- `<related-location label="..." path="..." start-line="..." end-line="..."/>`
  - Self-closing. `label`, `path` and `start-line` are required.
  - Put them together at the end of their section; they show as related files.
- Escape `&` `"` `'` `<` `>` inside attribute values. Tag names written in
  inline code or code blocks stay prose.
