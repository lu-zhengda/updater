------------------------------ MODULE SelfUpdate ------------------------------
\* Safety model of the in-app self-update helper.
\*
\* One step here is one step in cmd/updater/self_upgrade.go (applySelfUpgrade
\* and the functions it calls). Line numbers are the file at the sync commit
\* recorded in SKILL.md. Entry points that reach this helper:
\*   cmd/updater/upgrade.go runUpgrade, when the executable is inside Updater.app
\*   cmd/updater/update.go executeUpdate, when the bundle id is Updater's
\*   cmd/updater/menubar_app.go runUpdate shells `update --bundle-id`
\*
\* Versions are 1 (what is installed at the start), 2, and 3 (newer releases).
\* 0 means the install path has no bundle. appOK means the bundle at that path
\* passed the checksum and Developer ID checks, or it is the original app.
\*
\* TwoRename, SkipChecksum, SkipSignature, SkipNewer, SkipRelock, SkipLock,
\* SkipVersionMatch, and SkipCaskVersion are the broken variants. They are
\* FALSE for the code as it is. SkipRecovery removes the next-run restore.
\* BrewFault is "none" unless a config is showing a Homebrew outcome.
\* "regress" means brew info can report a version other than the GitHub tag.
\* The cask-version guard refuses a lower one, so brew is not asked to install
\* it. "partial" and "unsigned" are brew's own replace: the path can be empty
\* while brew has not returned, and a kill after brew returns can leave a
\* bundle that this process has not checked. Those two states are accepted
\* boundaries. LaunchRecover is the next run or launch. It does not erase them.

EXTENDS Integers, FiniteSets

CONSTANTS
  Helpers,
  Mode,              \* "direct" (tarball) or "brew" (cask)
  BrewFault,         \* "none", "partial", "unsigned", "regress"
  TwoRename,
  SkipChecksum,
  SkipSignature,
  SkipNewer,
  SkipRelock,
  SkipLock,
  SkipVersionMatch,
  SkipCaskVersion,
  SkipRecovery

ASSUME Mode \in {"direct", "brew"}
ASSUME BrewFault \in {"none", "partial", "unsigned", "regress"}
ASSUME TwoRename \in BOOLEAN /\ SkipChecksum \in BOOLEAN /\ SkipSignature \in BOOLEAN
ASSUME SkipNewer \in BOOLEAN /\ SkipRelock \in BOOLEAN /\ SkipLock \in BOOLEAN
ASSUME SkipVersionMatch \in BOOLEAN /\ SkipCaskVersion \in BOOLEAN
ASSUME SkipRecovery \in BOOLEAN

VARIABLES
  appVer,    \* 0 = path empty; 1, 2, 3 = bundle version at the install path
  appOK,     \* checksum and Developer ID checks passed for that bundle
  trusted,   \* highest version successfully committed
  lock,      \* "free" or the helper that holds update.lock exclusively
  shared,    \* a batch holds the shared lock (updateActivityLock false)
  pc,        \* helper phase
  rel,       \* release version this helper fetched
  cand       \* staged candidate: ver, sum (checksum ok), sig (Developer ID ok)

vars == <<appVer, appOK, trusted, lock, shared, pc, rel, cand>>

Phases == {
  "idle", "fetched", "downloaded", "extracted", "locked", "rechecked",
  "verified", "stopped", "guarded", "brewing", "gap", "brewed", "rollback",
  "done", "failed", "dead"
}

TypeOK ==
  /\ appVer \in 0..3
  /\ appOK \in BOOLEAN
  /\ trusted \in 1..3
  /\ lock = "free" \/ lock \in Helpers
  /\ shared \in BOOLEAN
  /\ pc \in [Helpers -> Phases]
  /\ rel \in [Helpers -> 0..3]
  /\ cand \in [Helpers -> [ver: 0..3, sum: BOOLEAN, sig: BOOLEAN]]

Init ==
  /\ appVer = 1
  /\ appOK = TRUE
  /\ trusted = 1
  /\ lock = "free"
  /\ shared = FALSE
  /\ pc = [h \in Helpers |-> "idle"]
  /\ rel = [h \in Helpers |-> 0]
  /\ cand = [h \in Helpers |-> [ver |-> 0, sum |-> FALSE, sig |-> FALSE]]

\* Drop the exclusive lock if this helper holds it.
Release(h) == IF lock = h THEN lock' = "free" ELSE UNCHANGED lock

\* pc and the exclusive lock only. Callers prime every other variable.
Fail(h) ==
  /\ pc' = [pc EXCEPT ![h] = "failed"]
  /\ Release(h)

Done(h) ==
  /\ pc' = [pc EXCEPT ![h] = "done"]
  /\ Release(h)

SameApp == UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>

\* readUpdaterBundle (self_upgrade.go:118) then selfUpgradeRelease and the
\* first IsNewer (applySelfUpgrade, self_upgrade.go:324). A missing bundle
\* fails the read. recoverStrandedUpdater (self_upgrade.go:265) runs before
\* that read, on this command and on launch (recoverLauncherBundle). A release
\* that is not newer returns "already up to date".
ReadAndFetch(h) ==
  /\ pc[h] = "idle"
  /\ \E v \in {2, 3}:
       /\ rel' = [rel EXCEPT ![h] = v]
       /\ IF appVer = 0
            THEN /\ Fail(h)
                 /\ UNCHANGED <<appVer, appOK, trusted, shared, cand>>
            ELSE IF ~SkipNewer /\ v <= appVer
                   THEN /\ Done(h)
                        /\ UNCHANGED <<appVer, appOK, trusted, shared, cand>>
                   ELSE /\ pc' = [pc EXCEPT ![h] = "fetched"]
                        /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, cand>>

\* downloadSelfArchive (self_upgrade.go:551). Checksum reject is line 593.
\* Network drop is a failed download. The installed app is unchanged.
\* SkipChecksum keeps an archive that failed the checksum.
DownloadArchive(h) ==
  /\ pc[h] = "fetched"
  /\ Mode = "direct"
  /\ \E o \in {"good", "badSum", "badSig", "mismatch", "drop"}:
       \/ /\ o = "drop" /\ Fail(h) /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>
       \/ /\ o = "badSum" /\ ~SkipChecksum /\ Fail(h) /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>
       \/ /\ o = "badSum" /\ SkipChecksum
            /\ pc' = [pc EXCEPT ![h] = "downloaded"]
            /\ cand' = [cand EXCEPT ![h] = [ver |-> rel[h], sum |-> FALSE, sig |-> TRUE]]
            /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel>>
       \/ /\ o = "badSig"
            /\ pc' = [pc EXCEPT ![h] = "downloaded"]
            /\ cand' = [cand EXCEPT ![h] = [ver |-> rel[h], sum |-> TRUE, sig |-> FALSE]]
            /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel>>
       \/ /\ o = "mismatch" /\ ~SkipVersionMatch /\ Fail(h) /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>
       \/ /\ o = "mismatch" /\ SkipVersionMatch
            /\ pc' = [pc EXCEPT ![h] = "downloaded"]
            /\ cand' = [cand EXCEPT ![h] = [ver |-> 1, sum |-> TRUE, sig |-> TRUE]]
            /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel>>
       \/ /\ o = "good"
            /\ pc' = [pc EXCEPT ![h] = "downloaded"]
            /\ cand' = [cand EXCEPT ![h] = [ver |-> rel[h], sum |-> TRUE, sig |-> TRUE]]
            /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel>>

\* The cask path does not download the tarball (applySelfUpgrade, the
\* `if !brew` block). Brew owns the install. This process does not copy a
\* bundle into the Caskroom or rewrite the receipt.
BrewSkipDownload(h) ==
  /\ pc[h] = "fetched"
  /\ Mode = "brew"
  /\ pc' = [pc EXCEPT ![h] = "extracted"]
  /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* extractUpdaterApp (self_upgrade.go:604) and the bundle-version check
\* (self_upgrade.go:359). A plist version other than the release is refused
\* unless SkipVersionMatch.
ExtractAndMatch(h) ==
  /\ pc[h] = "downloaded"
  /\ IF cand[h].ver = rel[h] \/ SkipVersionMatch
       THEN /\ pc' = [pc EXCEPT ![h] = "extracted"]
            /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>
       ELSE /\ Fail(h)
            /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>

\* updateActivityLock(ctx, true) (self_upgrade.go:167, called at line 358).
\* The same lock is held from here until brew has returned and the post-checks
\* (or the direct swap) finish. Shared holders and the other helper's
\* exclusive lock block it. SkipLock is the broken variant that replaces
\* without update.lock.
AcquireLock(h) ==
  /\ pc[h] = "extracted"
  /\ \/ /\ ~SkipLock
        /\ lock = "free"
        /\ ~shared
        /\ lock' = h
        /\ pc' = [pc EXCEPT ![h] = "locked"]
        /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>
     \/ /\ SkipLock
        /\ pc' = [pc EXCEPT ![h] = "locked"]
        /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* The second readUpdaterBundle and IsNewer (self_upgrade.go:364).
\* SkipRelock removes only this check. SkipNewer removes both IsNewer calls.
RereadAndRecheck(h) ==
  /\ pc[h] = "locked"
  /\ IF appVer = 0
       THEN /\ Fail(h) /\ SameApp
       ELSE IF ~SkipNewer /\ ~SkipRelock /\ rel[h] <= appVer
              THEN /\ Done(h) /\ SameApp
              ELSE /\ pc' = [pc EXCEPT ![h] = "rechecked"]
                   /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* verifySelfUpgrade -> signing.VerifyReplacementApp (self_upgrade.go:391,
\* verify.go:40). Team, identifier, codesign, and Gatekeeper are one gate.
\* SkipSignature installs anyway. A bad checksum that was kept only by
\* SkipChecksum is rejected here unless that mutation is on.
VerifySignature(h) ==
  /\ pc[h] = "rechecked"
  /\ Mode = "direct"
  /\ IF ((cand[h].sum \/ SkipChecksum) /\ (cand[h].sig \/ SkipSignature))
       THEN /\ pc' = [pc EXCEPT ![h] = "verified"]
            /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>
       ELSE /\ Fail(h) /\ SameApp

\* stopUpdaterUI (self_upgrade.go:529, called at line 430). The bundle is
\* still the old one. Direct path only: brew stops the UI after the guard.
StopUI(h) ==
  /\ pc[h] = "verified"
  /\ pc' = [pc EXCEPT ![h] = "stopped"]
  /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* caskVersionAllowed (self_upgrade.go:211), called under the lock at line 382
\* before the ditto and before brew upgrade. readCaskVersion is brew info
\* (line 220). A cask version lower than the fetched release or the installed
\* app is refused. BrewFault "regress" is the case where that version can be
\* 1, 2, or 3, not the tag. Other faults report the fetched release.
\* SkipCaskVersion is the guard removed.
BrewGuard(h) ==
  /\ pc[h] = "rechecked"
  /\ Mode = "brew"
  /\ \E c \in 1..3:
       /\ \/ BrewFault = "regress"
          \/ c = rel[h]
       /\ IF (~SkipCaskVersion /\ (c < rel[h] \/ c < appVer))
            THEN /\ Fail(h)
                 /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>
            ELSE /\ pc' = [pc EXCEPT ![h] = "guarded"]
                 /\ cand' = [cand EXCEPT ![h] = [ver |-> c, sum |-> TRUE, sig |-> TRUE]]
                 /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel>>

\* Cask path: ditto backup (self_upgrade.go:397) after the guard. It does not
\* change the installed bundle. The lock is still held. stopUpdaterUI follows
\* in the same phase; the bundle is still the old one.
BrewPrepare(h) ==
  /\ pc[h] = "guarded"
  /\ Mode = "brew"
  /\ pc' = [pc EXCEPT ![h] = "stopped"]
  /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* replaceUpdaterBundle (self_upgrade.go:478). One RENAME_SWAP. appOK is the
\* candidate's real checksum and signature, so a skipped check that still
\* installs records a bad asset.
AtomicSwap(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "direct"
  /\ ~TwoRename
  /\ appVer' = cand[h].ver
  /\ appOK' = (cand[h].sum /\ cand[h].sig)
  /\ IF (cand[h].sum /\ cand[h].sig /\ cand[h].ver > trusted)
       THEN trusted' = cand[h].ver
       ELSE UNCHANGED trusted
  /\ Done(h)
  /\ UNCHANGED <<shared, rel, cand>>

\* The old replaceUpdaterBundle: os.Rename(bundle, backup) then
\* os.Rename(candidate, bundle). The state after the first rename is the
\* counterexample. RenameIn and RestoreGap are the second rename and its
\* restore; AlwaysPresent fails before they run.
RenameAway(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "direct"
  /\ TwoRename
  /\ appVer' = 0
  /\ pc' = [pc EXCEPT ![h] = "gap"]
  /\ UNCHANGED <<appOK, trusted, lock, shared, rel, cand>>

RenameIn(h) ==
  /\ pc[h] = "gap"
  /\ Mode = "direct"
  /\ appVer' = cand[h].ver
  /\ appOK' = (cand[h].sum /\ cand[h].sig)
  /\ IF (cand[h].sum /\ cand[h].sig /\ cand[h].ver > trusted)
       THEN trusted' = cand[h].ver
       ELSE UNCHANGED trusted
  /\ Done(h)
  /\ UNCHANGED <<shared, rel, cand>>

RestoreGap(h) ==
  /\ pc[h] = "gap"
  /\ Mode = "direct"
  /\ appVer' = trusted
  /\ appOK' = TRUE
  /\ UNCHANGED trusted
  /\ Done(h)
  /\ UNCHANGED <<shared, rel, cand>>

\* brew upgrade --cask (self_upgrade.go:441). The version installed is the
\* cask version the guard just accepted (cand.ver). "partial" removes the
\* bundle and does not return (phase brewing). "unsigned" returns a bundle
\* this process has not checked yet (phase brewed, appOK false). A kill in
\* those two phases is the accepted boundary. BrewUpgradeFails is brew
\* returning an error without changing the bundle.
BrewUpgradeGood(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "brew"
  /\ appVer' = cand[h].ver
  /\ appOK' = TRUE
  /\ IF cand[h].ver > trusted THEN trusted' = cand[h].ver ELSE UNCHANGED trusted
  /\ pc' = [pc EXCEPT ![h] = "brewed"]
  /\ UNCHANGED <<lock, shared, rel, cand>>

BrewUpgradeFails(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "brew"
  /\ pc' = [pc EXCEPT ![h] = "rollback"]
  /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

BrewUpgradePartial(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "brew"
  /\ BrewFault = "partial"
  /\ appVer' = 0
  /\ pc' = [pc EXCEPT ![h] = "brewing"]
  /\ UNCHANGED <<appOK, trusted, lock, shared, rel, cand>>

BrewUpgradeUnsigned(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "brew"
  /\ BrewFault = "unsigned"
  /\ appVer' = cand[h].ver
  /\ appOK' = FALSE
  /\ pc' = [pc EXCEPT ![h] = "brewed"]
  /\ UNCHANGED <<trusted, lock, shared, rel, cand>>

\* Post-upgrade bundle-version and Developer ID checks (self_upgrade.go:436).
\* Failure goes to rollback. The brewed state itself is the kill window.
BrewCheck(h) ==
  /\ pc[h] = "brewed"
  /\ IF appOK /\ appVer \in 1..3 /\ appVer >= trusted
       THEN /\ Done(h)
            /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>
       ELSE /\ pc' = [pc EXCEPT ![h] = "rollback"]
            /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* restoreUpdaterBundle (self_upgrade.go:492). One RENAME_SWAP when the
\* failed bundle is still there. The backup is the last committed version.
\* TwoRename is the old two-os.Rename rollback: move the bundle aside, then
\* move the backup in.
AtomicRollback(h) ==
  /\ pc[h] = "rollback"
  /\ ~TwoRename
  /\ appVer' = trusted
  /\ appOK' = TRUE
  /\ UNCHANGED trusted
  /\ Done(h)
  /\ UNCHANGED <<shared, rel, cand>>

RollbackAway(h) ==
  /\ pc[h] = "rollback"
  /\ TwoRename
  /\ appVer' = 0
  /\ pc' = [pc EXCEPT ![h] = "gap"]
  /\ UNCHANGED <<appOK, trusted, lock, shared, rel, cand>>

\* Kill or crash. The kernel drops the flock. Defers do not run, so a gap
\* or a bad brew result stays. The installed app is otherwise untouched.
Kill(h) ==
  /\ pc[h] \notin {"done", "failed", "dead"}
  /\ pc' = [pc EXCEPT ![h] = "dead"]
  /\ Release(h)
  /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>

\* Another update/batch holds the shared lock (update.go executeUpdate,
\* updateActivityLock(ctx, false)). It excludes the exclusive self-update lock.
GrabShared ==
  /\ ~shared
  /\ lock = "free"
  /\ shared' = TRUE
  /\ UNCHANGED <<appVer, appOK, trusted, lock, pc, rel, cand>>

ReleaseShared ==
  /\ shared
  /\ shared' = FALSE
  /\ UNCHANGED <<appVer, appOK, trusted, lock, pc, rel, cand>>

\* recoverStrandedUpdater (self_upgrade.go:265) and recoverLauncherBundle
\* (self_upgrade.go:294). The next self-upgrade run or the next launch finds
\* the app missing or unsigned and the ditto backup present, and restores the
\* last committed version. SkipRecovery is that restore removed. This action
\* does not run while a helper is inside brew, and it does not delete the
\* earlier state where the app was already missing or unchecked.
LaunchRecover ==
  /\ Mode = "brew"
  /\ ~SkipRecovery
  /\ \A h \in Helpers: pc[h] \in {"idle", "done", "failed", "dead"}
  /\ \/ appVer = 0
     \/ ~appOK
  /\ appVer' = trusted
  /\ appOK' = TRUE
  /\ UNCHANGED <<trusted, lock, shared, pc, rel, cand>>

Next ==
  \/ \E h \in Helpers: ReadAndFetch(h)
  \/ \E h \in Helpers: DownloadArchive(h)
  \/ \E h \in Helpers: BrewSkipDownload(h)
  \/ \E h \in Helpers: ExtractAndMatch(h)
  \/ \E h \in Helpers: AcquireLock(h)
  \/ \E h \in Helpers: RereadAndRecheck(h)
  \/ \E h \in Helpers: VerifySignature(h)
  \/ \E h \in Helpers: StopUI(h)
  \/ \E h \in Helpers: BrewPrepare(h)
  \/ \E h \in Helpers: BrewGuard(h)
  \/ \E h \in Helpers: AtomicSwap(h)
  \/ \E h \in Helpers: RenameAway(h)
  \/ \E h \in Helpers: RenameIn(h)
  \/ \E h \in Helpers: RestoreGap(h)
  \/ \E h \in Helpers: BrewUpgradeGood(h)
  \/ \E h \in Helpers: BrewUpgradeFails(h)
  \/ \E h \in Helpers: BrewUpgradePartial(h)
  \/ \E h \in Helpers: BrewUpgradeUnsigned(h)
  \/ \E h \in Helpers: BrewCheck(h)
  \/ \E h \in Helpers: AtomicRollback(h)
  \/ \E h \in Helpers: RollbackAway(h)
  \/ \E h \in Helpers: Kill(h)
  \/ GrabShared
  \/ ReleaseShared
  \/ LaunchRecover

Spec == Init /\ [][Next]_vars

\* The install path always has a complete bundle.
AlwaysPresent == appVer \in {1, 2, 3}

\* A committed version is never replaced by an older one. appVer = 0 is
\* AlwaysPresent's failure, not a downgrade.
NoDowngrade == appVer = 0 \/ appVer >= trusted

\* The bundle at the path passed checksum and Developer ID checks.
NoBadInstall == appOK

\* Two helpers do not both hold the exclusive lock. SkipLock is the variant
\* that does not take it.
MutualExclusion ==
  SkipLock \/ Cardinality({h \in Helpers: lock = h}) <= 1

\* update.lock is held for the whole replacement, including the brew
\* invocation, the cask-version guard, and the post-checks. A kill drops it.
LockSpansBrew ==
  \A h \in Helpers:
    pc[h] \in {"locked", "rechecked", "verified", "stopped", "guarded",
               "brewing", "brewed", "rollback", "gap"}
    => SkipLock \/ lock = h

=============================================================================
