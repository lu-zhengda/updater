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
\* TwoRename, SkipChecksum, SkipSignature, SkipNewer, SkipRelock, SkipLock, and
\* SkipVersionMatch are the broken variants. They are FALSE for the code as it
\* is. BrewFault is "none" unless a config is showing a Homebrew outcome the
\* helper does not roll back before a kill.

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
  SkipVersionMatch

ASSUME Mode \in {"direct", "brew"}
ASSUME BrewFault \in {"none", "partial", "unsigned", "regress"}
ASSUME TwoRename \in BOOLEAN /\ SkipChecksum \in BOOLEAN /\ SkipSignature \in BOOLEAN
ASSUME SkipNewer \in BOOLEAN /\ SkipRelock \in BOOLEAN /\ SkipLock \in BOOLEAN
ASSUME SkipVersionMatch \in BOOLEAN

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
  "verified", "stopped", "gap", "brewed", "rollback", "done", "failed", "dead"
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

\* readUpdaterBundle (self_upgrade.go:115) then selfUpgradeRelease and the
\* first IsNewer (applySelfUpgrade, self_upgrade.go:205-219). A missing bundle
\* fails the read. A release that is not newer returns "already up to date".
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

\* downloadSelfArchive (self_upgrade.go:426). Checksum reject is line 467.
\* Network drop is a failed downloadFileAndHash (line 463). The installed app
\* is unchanged. SkipChecksum keeps an archive that failed line 467.
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
\* `if !brew` block at self_upgrade.go:235).
BrewSkipDownload(h) ==
  /\ pc[h] = "fetched"
  /\ Mode = "brew"
  /\ pc' = [pc EXCEPT ![h] = "extracted"]
  /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* extractUpdaterApp (self_upgrade.go:479) and the bundle-version check
\* (self_upgrade.go:247). A plist version other than the release is refused
\* unless SkipVersionMatch.
ExtractAndMatch(h) ==
  /\ pc[h] = "downloaded"
  /\ IF cand[h].ver = rel[h] \/ SkipVersionMatch
       THEN /\ pc' = [pc EXCEPT ![h] = "extracted"]
            /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>
       ELSE /\ Fail(h)
            /\ UNCHANGED <<appVer, appOK, trusted, shared, rel, cand>>

\* updateActivityLock(ctx, true) (self_upgrade.go:164, called at line 252).
\* Shared holders and the other helper's exclusive lock block it. SkipLock
\* is the broken variant that replaces without update.lock.
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

\* The second readUpdaterBundle and IsNewer (self_upgrade.go:258-263).
\* SkipRelock removes only this check. SkipNewer removes both IsNewer calls.
RereadAndRecheck(h) ==
  /\ pc[h] = "locked"
  /\ IF appVer = 0
       THEN /\ Fail(h) /\ SameApp
       ELSE IF ~SkipNewer /\ ~SkipRelock /\ rel[h] <= appVer
              THEN /\ Done(h) /\ SameApp
              ELSE /\ pc' = [pc EXCEPT ![h] = "rechecked"]
                   /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* verifySelfUpgrade -> signing.VerifyReplacementApp (self_upgrade.go:266,
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

\* stopUpdaterUI (self_upgrade.go:404, called at line 305). The bundle is
\* still the old one.
StopUI(h) ==
  /\ pc[h] = "verified"
  /\ pc' = [pc EXCEPT ![h] = "stopped"]
  /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* Cask path: ditto backup (self_upgrade.go:272) does not change the installed
\* bundle, and signature is not checked before brew upgrade.
BrewPrepare(h) ==
  /\ pc[h] = "rechecked"
  /\ Mode = "brew"
  /\ pc' = [pc EXCEPT ![h] = "stopped"]
  /\ UNCHANGED <<appVer, appOK, trusted, lock, shared, rel, cand>>

\* replaceUpdaterBundle (self_upgrade.go:353). One RENAME_SWAP. appOK is the
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

\* brew upgrade --cask (self_upgrade.go:316). "none" is a brew that either
\* installs the newer signed release or returns an error without changing
\* the bundle. The other faults are results brew can leave behind before
\* the post-upgrade IsNewer (line 320) and verifySelfUpgrade (line 324).
BrewUpgradeGood(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "brew"
  /\ appVer' = rel[h]
  /\ appOK' = TRUE
  /\ IF rel[h] > trusted THEN trusted' = rel[h] ELSE UNCHANGED trusted
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
  /\ pc' = [pc EXCEPT ![h] = "brewed"]
  /\ UNCHANGED <<appOK, trusted, lock, shared, rel, cand>>

BrewUpgradeUnsigned(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "brew"
  /\ BrewFault = "unsigned"
  /\ appVer' = rel[h]
  /\ appOK' = FALSE
  /\ pc' = [pc EXCEPT ![h] = "brewed"]
  /\ UNCHANGED <<trusted, lock, shared, rel, cand>>

BrewUpgradeRegress(h) ==
  /\ pc[h] = "stopped"
  /\ Mode = "brew"
  /\ BrewFault = "regress"
  /\ appVer' = 1
  /\ appOK' = TRUE
  /\ pc' = [pc EXCEPT ![h] = "brewed"]
  /\ UNCHANGED <<trusted, lock, shared, rel, cand>>

\* Post-upgrade checks passed (self_upgrade.go:320-324). The good app is
\* already at the path.
BrewAccept(h) ==
  /\ pc[h] = "brewed"
  /\ appOK
  /\ appVer \in 1..3
  /\ Done(h)
  /\ SameApp

\* restoreUpdaterBundle (self_upgrade.go:367). One RENAME_SWAP when the
\* failed bundle is still there. TwoRename is the old two-os.Rename rollback:
\* move the bundle aside, then move the backup in.
AtomicRollback(h) ==
  /\ pc[h] = "rollback"
  /\ ~TwoRename
  /\ Done(h)
  /\ SameApp

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
  \/ \E h \in Helpers: AtomicSwap(h)
  \/ \E h \in Helpers: RenameAway(h)
  \/ \E h \in Helpers: RenameIn(h)
  \/ \E h \in Helpers: RestoreGap(h)
  \/ \E h \in Helpers: BrewUpgradeGood(h)
  \/ \E h \in Helpers: BrewUpgradeFails(h)
  \/ \E h \in Helpers: BrewUpgradePartial(h)
  \/ \E h \in Helpers: BrewUpgradeUnsigned(h)
  \/ \E h \in Helpers: BrewUpgradeRegress(h)
  \/ \E h \in Helpers: BrewAccept(h)
  \/ \E h \in Helpers: AtomicRollback(h)
  \/ \E h \in Helpers: RollbackAway(h)
  \/ \E h \in Helpers: Kill(h)
  \/ GrabShared
  \/ ReleaseShared

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

=============================================================================
