## Commit and Merge Rules

- If you notice unfamiliar or unrelated file changes, continue working and stay focused on your own scoped edits unless the user asks you to inspect them.
- For each completed round that modifies existing code/docs or adds new code/docs, automatically stage and commit only the files you changed and understand.
- For deletion changes, wait until the user explicitly says "commit" before staging and committing those deletions.
- Prefer small, frequent commits. Commit each independent, verifiable unit of work as soon as it is complete.
- For every modification or newly added feature, create one commit.
- For every commit, use the `metabot-post-buzz` skill to post a detailed development-journal entry in English on-chain describing the change.
- Use commit messages in the format `<type>: <short description>`, where `<type>` is one of `feat`, `fix`, `refactor`, `docs`, or `chore`.
- Before committing, make sure the relevant local tests or verification steps pass for your changes.
- When merging completed work into `main`, use `git merge --no-ff` to preserve the feature merge point.
