# Account deletion

`services.AccountDeletionService` deletes an account's data, as Mastodon's
`DeleteAccountService` does. `DELETE /api/v1/admin/accounts/:id` starts it in
the background for an account that a moderator suspended. A moderator's
suspension leaves a deletion request; the endpoint refuses accounts without
one, as Mastodon's `AccountPolicy#destroy?` does.

## What a deletion does

The service runs only on an account that is suspended and still has its
deletion request. For any other account it does nothing. It then works
through these steps in order:

1. **Disable the user.** A local account's user is disabled, its unused
   invites are deleted, its access tokens and grants are revoked, and its
   sessions and Web Push subscriptions are deleted.
2. **Federate.** For a local account, its actor's `Delete` (id
   `<actor>#delete`) is signed by the account. It goes first to its
   followers' inboxes, then to every other known inbox of a remote account
   that isn't suspended. For a remote account, each follow and follow request
   it has of a local account gets a `Reject`. Each follow that a local account
   has of it gets an `Undo`. Both go to its inbox, signed by the local
   account. Each follow is deleted once its activity is queued.
3. **Empty the profile.** The profile is cleared: no name, bio, fields,
   aliases or counts, and the avatar and header files are deleted. The account
   stays suspended by this server, under its username, URI and keys.
4. **Purge content.** The service deletes the account's statuses, in batches
   of 100, one transaction per batch. Others' reblogs of them go too. Direct
   statuses are unlinked from their local recipients' conversations. Live
   replies are taken out of the reply counts of other accounts' statuses.
   After the statuses, it deletes the account's:
   - mentions, media attachments and their files, and polls;
   - notifications, both its own and the ones it caused, and notification
     requests;
   - favourites, which are also taken out of the statuses' counts, and
     bookmarks;
   - follows, follow requests, blocks and mutes, in both directions;
   - conversations, notes, endorsements, aliases, filters, domain blocks,
     featured tags, list memberships, lists, migrations, report notes,
     scheduled statuses, pins, followed tags, notification permissions,
     trends and its status cleanup policy.

   Each of these deletes takes at most 1,000 rows per statement.
5. **Finish.** The deletion request is deleted last.

## What is kept

A moderator's deletion keeps both the username and the e-mail address:

- **The account record.** It stays suspended with no deletion request, so
  webfinger and the actor answer `410 Gone` and the username stays taken.
- **The user record.** It stays disabled, so the e-mail address can't sign up
  again.
- **Reported statuses.** Statuses named by unresolved reports about the
  account are kept, with their mentions, media and polls. Deleting the
  account's status cleanup policy keeps the cleanup worker away from them.
- **Moderation history.** Reports, moderation notes, warnings, appeals and
  the admin action log about the account are kept.
- **Others' data about the account.** Other accounts' notes about it,
  endorsements of it and lists are theirs, and stay.

An owner's deletion of their own account (Mastodon's `AccountDeletionWorker`)
runs with `ReserveEmail` off, which deletes the user at the end.

The service deletes a local account whose user never confirmed its e-mail
address, or was never approved, in full. It sends no activities and keeps
nothing. Deleting the record removes the rest through foreign keys.

## Failures and retries

The service stops at the first failure and returns it. It keeps the deletion
request, so a later run picks up where this one stopped:

- Every step reads what is left, so work that finished is not done again.
- A status batch that fails rolls back completely.
- A media file is deleted before its row. A run that can't delete the file
  leaves the row for the retry.
- A follow is deleted only after its `Reject` or `Undo` is queued, so a retry
  sends no activity twice and loses none.
- The actor `Delete` is sent again on every run, with the same id.
- A run on an account whose deletion has finished does nothing.

With workers enabled, the deletion is the Asynq task `account:delete` on the
`low` queue:

- **Attempts.** Each attempt has a one-hour timeout. An attempt that runs out
  stops, and the retry continues from there.
- **Retries.** A task is retried up to 25 times, about three weeks.
- **Duplicates.** While a task for an account is queued or retrying, a
  second request for the same account is dropped. This lasts up to seven
  days.

With workers disabled, the deletion runs once in the server process. A
second request for an account whose deletion is running starts nothing. A
failure is logged. Sending `DELETE` again resumes the deletion, because the
account keeps its deletion request until the deletion finishes.
