# Unix forwarding prototype: native source-adapter evidence

The reviewed source-side Unix adapter prototype at `75a042e32f2280a8c0469f2dc907508023b23881` passed one native macOS arm64 race gate on October 27, 2026. The ordinary build does not enable this dual-tag prototype. This receipt covers the prototype data path and its exact source revision; it does not establish production forwarding authority.

## Gate and source identity

The unchanged normal FIFO admitted one run with its default two slots and 1,800-second limit. All queue overrides were unset. The gate pinned a clean Git commit, all 2,416 tracked files and their selected Git blobs, ten prototype source files, the queue wrapper, selected tool inputs and its own harness before building. Those inputs, the newly built race image and its Go build information matched again after the run.

| Evidence | SHA-256 or result |
| --- | --- |
| Tested source commit | `75a042e32f2280a8c0469f2dc907508023b23881` |
| Ten-file prototype source manifest | `e133a207a50fb719c14a687784c7b7b33fecd4c4c81789bd7de7d1470be625de` |
| All-tracked-file manifest | `d291aef33e1b37622b701310d0710eec89abfdc8504ffaab9ebf6d657fc52d9d` |
| Selected Git-blob manifest | `340b81a7e2aa9fb242aca154b4a4616e935e5d7395d4f145dec86800910ce901` |
| Selected Go/Xcode/SDK input snapshot | `a982c0a27df695e7d1c2897df80a8f624b024ab97067480837a47d2b1eb8a925` |
| Normal FIFO wrapper | `769e8980b0e10ad8724eef473c463acf0d572dfe789d05ca008482d03f757922` |
| Gate script | `5fa864563055252e01e3274b68a039ebc6acfb9027fc0e47aee3cafd70bb5329` |
| Outer command, terminal and direct-wait receipt manifest | `86923fea6d50a8d3a10fc56339193aa992fe93ed8e16ac5c7efaaff2fa987384` |
| Newly built race test image | `f52372ffca6d5c43021648b3275b68fa3e86d5ada736536d3fee6df4858a613f` |
| Exact 31-name test roster | `4b63fb0f101b74e455ff5cd811845a0447b48a012abb56a22cf5ef411c9d3456` |
| Raw targeted test output | `151b9a49288b13152d0e41c54c5905b1cbe28bbd77a02d1f4901cd9570f90cbb` |

Go 1.27.3 built the Darwin arm64 image with `-race`, CGO enabled and `arm_forward_review,arm_forward_unix_prototype`. The direct child wait receipts for the race build, tagged vet, roster listing and targeted test each report exit `0`. The roster matched the pinned 31 names, and the one `-test.count=1 -test.timeout=120s` run passed all 31 top-level cases with no failure or race report. The outer FIFO command also exited `0`. The direct waits establish the immediate commands' exits, not completion of every possible descendant.

The causal deadline cases passed separately: an opener returning before the deadline kept its stream alive after the window, while an opener returning after the deadline with an already-started, delayed timer callback was refused before application bytes crossed. The latter test covers the specific race fixed at this revision. Other cases cover pending admission, native socket and peer controls, bounded pair and adapter counts, partial opener ownership, cancellation, stream errors and exact cleanup behavior. The complete roster is:

```text
TestUnixPrototypeAdmissionBeforeApplicationBytesAndCancellationJoin
TestUnixPrototypeCanceledCopyPreservesIndependentEIO
TestUnixPrototypeCancellationErrorTreeBound
TestUnixPrototypeChangedInodeAndCloseFailureRetainPath
TestUnixPrototypeCopyBoundsChunksAndHidesFastPaths
TestUnixPrototypeCopyErrorAbortsBothOriginalDirections
TestUnixPrototypeCopyOnlyLiteralEOFIsSuccess
TestUnixPrototypeDarwinSocketCreationUsesForkLock
TestUnixPrototypeEarlyCancelHasNoFilesystemEffect
TestUnixPrototypeEarlyOpenJoinsTimerWithoutStreamDeadline
TestUnixPrototypeFailedSetupKeepsOriginalOwner
TestUnixPrototypeGlobalPendingCapAcrossHomes
TestUnixPrototypeListenerBufferReadbackWithinCeiling
TestUnixPrototypeLostAckAndUnjoinedSameProcessPathRefuseAdoption
TestUnixPrototypeMixedEOFStreamAbortsAndPreservesOutcome
TestUnixPrototypeNamedHomeReplacementRetainsOriginalPath
TestUnixPrototypeNativePeerUIDWrongExpectedControl
TestUnixPrototypeOriginalCloseErrorIsStickyAndRetainsPath
TestUnixPrototypePartialAdmissionConnectionClosedOnce
TestUnixPrototypePendingOpenerDeadlineClosesWithoutReadingClient
TestUnixPrototypePendingPairsOccupyCapUntilOpenerJoins
TestUnixPrototypePhysicalHomeCaseAliasCapDarwin
TestUnixPrototypeProcessAdapterCapAndExactRecycle
TestUnixPrototypeQueuedByteProbeDetectsPrematureRead
TestUnixPrototypeRawSocketHasCloexecBeforeBind
TestUnixPrototypeRejectedCloseFailureKeepsOneEmergencyOwner
TestUnixPrototypeRejectsHomeAliasesBeforeReservation
TestUnixPrototypeSequentialFailuresRecycleSlotsAndBoundOutcome
TestUnixPrototypeStartedTimerCallbackJoinsBeforeLateRefusal
TestUnixPrototypeStartedTimerCallbackJoinsOnCancellation
TestUnixPrototypeSyntheticUnclosedRemoteRetainsPairAndWithdrawsAdmission
```

## Cleanup and earlier results

The fixture creates adapter homes under the selected private temporary root and reports removal errors. Its post-test filesystem snapshot succeeded and found only that root, with no child paths. The root was retained as a receipt. This is evidence about the selected root at test exit, not proof that all host paths or processes were removed.

An earlier sandboxed run of source revision `37664e8` passed the race build, vet and roster but failed the test phase: seven top-level cases passed and 24 failed when the sandbox denied native Unix socket binding with `EPERM`. A later permitted run of that **earlier** image passed 31/31 cases. Both outcomes remain distinct. The earlier pass did not test the strict deadline correction in `75a042e`; the fresh image and gate above provide that successor evidence.

## Limits of the prototype

The per-home limit admits eight adapters for one verified physical source home, identified from its opened directory. The process-wide limit is 32 live, setup or retained owners. A failed provisional home attempt can leave the physical count above eight when its original close is uncertain; uncertainty before the home identity is read holds only a global charge. These are process-local prototype bounds, not a durable Manager lease or clock authority.

The deadline cases use shortened test timers to exercise the race; they do not measure a 15-second production service-level guarantee. The gate covers native macOS arm64 only; no Linux compile or native result is claimed here. The selected tool snapshot is not a full OS or module-cache attestation. The prototype does not include destination grants, production TLS admission, public CLI/RPC/schema, Manager recovery, or integrated relay/TCP transport. Production forwarding still needs those separately reviewed boundaries and gates.
