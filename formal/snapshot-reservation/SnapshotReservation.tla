----------------------- MODULE SnapshotReservation -----------------------
EXTENDS Naturals, TLC

(* One private key. deadlineOK is the latest actual clock observation, NOT a
   continuously live source or a monotonic-clock assumption. A successful check
   authorizes only this staged commit attempt. No native work/publication. *)
VARIABLES phase, fence, request, staged, pending, checked, cachePending,
          work, destination, pinned, owner, unknown, deadlineOK, nativeStarted
vars == <<phase,fence,request,staged,pending,checked,cachePending,
          work,destination,pinned,owner,unknown,deadlineOK,nativeStarted>>
Durable == <<phase,fence,request,work,destination,pinned>>
Admits == {"admit-A","admit-B"}
Stages == {"none","admit-A","admit-B","fence","cancel"}
Mutable == owner /\ ~unknown /\ ~cachePending /\ pending="none"

Init == /\ phase="none" /\ request="none" /\ fence=FALSE
        /\ staged="none" /\ pending="none" /\ checked=FALSE
        /\ cachePending=FALSE /\ work=FALSE /\ destination=FALSE
        /\ pinned=FALSE /\ owner=TRUE /\ unknown=FALSE
        /\ deadlineOK=TRUE /\ nativeStarted=FALSE
StageAdmit(d) ==
 /\ d \in {"A","B"} /\ Mutable /\ ~fence /\ phase="none" /\ staged="none"
 /\ staged'=IF d="A" THEN "admit-A" ELSE "admit-B"
 /\ UNCHANGED <<phase,fence,request,pending,checked,cachePending,work,
                 destination,pinned,owner,unknown,deadlineOK,nativeStarted>>
StageFence ==
 /\ Mutable /\ ~fence /\ phase="none" /\ staged="none" /\ staged'="fence"
 /\ UNCHANGED <<phase,fence,request,pending,checked,cachePending,work,
                 destination,pinned,owner,unknown,deadlineOK,nativeStarted>>
StageCancel ==
 /\ Mutable /\ phase="reserved" /\ staged="none" /\ staged'="cancel"
 /\ UNCHANGED <<phase,fence,request,pending,checked,cachePending,work,
                 destination,pinned,owner,unknown,deadlineOK,nativeStarted>>
ObserveValidity(v) ==
 /\ v \in BOOLEAN /\ deadlineOK'=v
 /\ UNCHANGED <<phase,fence,request,staged,pending,checked,cachePending,
                 work,destination,pinned,owner,unknown,nativeStarted>>
CheckAdmit ==
 /\ Mutable /\ staged \in Admits /\ ~checked /\ deadlineOK
 /\ checked'=TRUE
 /\ UNCHANGED <<phase,fence,request,staged,pending,cachePending,work,
                 destination,pinned,owner,unknown,deadlineOK,nativeStarted>>
BeginCommit ==
 /\ Mutable /\ staged#"none"
 /\ (staged \in Admits => checked /\ ~fence /\ phase="none")
 /\ pending'=staged /\ cachePending'=TRUE
 /\ UNCHANGED <<phase,fence,request,staged,checked,work,destination,pinned,
                 owner,unknown,deadlineOK,nativeStarted>>
FinishAdmit(ack) ==
 /\ owner /\ ~unknown /\ pending \in Admits /\ staged=pending /\ checked
 /\ phase="none" /\ ~fence
 /\ phase'="reserved" /\ request'=IF pending="admit-A" THEN "A" ELSE "B"
 /\ work'=TRUE /\ destination'=TRUE /\ pinned'=TRUE /\ unknown'=~ack
 /\ staged'="none" /\ pending'="none" /\ checked'=FALSE
 /\ UNCHANGED <<fence,cachePending,owner,deadlineOK,nativeStarted>>
FinishFence(ack) ==
 /\ owner /\ ~unknown /\ pending="fence" /\ staged="fence"
 /\ phase="none" /\ ~fence /\ fence'=TRUE /\ unknown'=~ack
 /\ staged'="none" /\ pending'="none" /\ checked'=FALSE
 /\ cachePending'=~ack
 /\ UNCHANGED <<phase,request,work,destination,pinned,owner,deadlineOK,nativeStarted>>
FinishCancel(ack) ==
 /\ owner /\ ~unknown /\ pending="cancel" /\ staged="cancel" /\ phase="reserved"
 /\ phase'="cancelled" /\ work'=FALSE /\ destination'=FALSE /\ pinned'=FALSE
 /\ unknown'=~ack /\ staged'="none" /\ pending'="none" /\ checked'=FALSE
 /\ UNCHANGED <<fence,request,cachePending,owner,deadlineOK,nativeStarted>>
UnknownWithoutWrite ==
 /\ owner /\ ~unknown /\ pending#"none"
 /\ unknown'=TRUE /\ pending'="none" /\ staged'="none" /\ checked'=FALSE
 /\ UNCHANGED <<phase,fence,request,cachePending,work,destination,pinned,
                 owner,deadlineOK,nativeStarted>>
PublicationUnknown ==
 /\ owner /\ ~unknown /\ cachePending /\ pending="none" /\ staged="none"
 /\ unknown'=TRUE
 /\ UNCHANGED <<phase,fence,request,staged,pending,checked,cachePending,
                 work,destination,pinned,owner,deadlineOK,nativeStarted>>
PublishKnown ==
 /\ owner /\ ~unknown /\ cachePending /\ pending="none" /\ staged="none"
 /\ cachePending'=FALSE
 /\ UNCHANGED <<phase,fence,request,staged,pending,checked,work,destination,
                 pinned,owner,unknown,deadlineOK,nativeStarted>>
Rollback ==
 /\ Mutable /\ staged#"none" /\ staged'="none" /\ checked'=FALSE
 /\ UNCHANGED <<phase,fence,request,pending,cachePending,work,destination,
                 pinned,owner,unknown,deadlineOK,nativeStarted>>
(* SQL mutation authority only: close/reload is not native process/FD absence.
   The actual producer records close errors separately. Reload requires a fresh
   runtime identity and a successful complete reader, not a readonly lookup. *)
CloseSQL ==
 /\ owner /\ staged="none" /\ pending="none"
 /\ owner'=FALSE /\ unknown'=TRUE
 /\ UNCHANGED <<phase,fence,request,staged,pending,checked,cachePending,
                 work,destination,pinned,deadlineOK,nativeStarted>>
ReloadSQL ==
 /\ ~owner /\ owner'=TRUE /\ unknown'=FALSE /\ cachePending'=FALSE
 /\ UNCHANGED <<phase,fence,request,staged,pending,checked,work,destination,
                 pinned,deadlineOK,nativeStarted>>
LookupOrRefusal == UNCHANGED vars
Next == (\E d \in {"A","B"}: StageAdmit(d)) \/ StageFence \/ StageCancel
     \/ (\E v \in BOOLEAN: ObserveValidity(v)) \/ CheckAdmit \/ BeginCommit
     \/ (\E ack \in BOOLEAN: FinishAdmit(ack) \/ FinishFence(ack) \/ FinishCancel(ack))
     \/ UnknownWithoutWrite \/ PublicationUnknown \/ PublishKnown \/ Rollback \/ CloseSQL \/ ReloadSQL
     \/ LookupOrRefusal
Spec == Init /\ [][Next]_vars
TypeOK ==
 /\ phase \in {"none","reserved","cancelled"} /\ request \in {"none","A","B"}
 /\ staged \in Stages /\ pending \in Stages
 /\ <<fence,checked,cachePending,work,destination,pinned,owner,unknown,
       deadlineOK,nativeStarted>> \in BOOLEAN \X BOOLEAN \X BOOLEAN \X BOOLEAN
       \X BOOLEAN \X BOOLEAN \X BOOLEAN \X BOOLEAN \X BOOLEAN \X BOOLEAN
AtomicReservation ==
 /\ work=destination /\ work=pinned /\ (phase="reserved")=work
 /\ (phase="none")=(request="none")
PendingShape ==
 /\ (pending#"none" => owner /\ ~unknown /\ cachePending /\ pending=staged)
 /\ (checked => staged \in Admits)
 /\ (staged#"none" => owner /\ ~unknown)
 /\ (staged="fence" => phase="none" /\ ~fence /\ ~checked)
 /\ (staged="cancel" => phase="reserved" /\ ~checked)
 /\ (staged \in Admits => phase="none" /\ ~fence)
 /\ (unknown => staged="none" /\ pending="none" /\ ~checked)
 /\ (~owner => staged="none" /\ pending="none" /\ unknown)
NoNativeWork == ~nativeStarted
Invariant == TypeOK /\ AtomicReservation /\ PendingShape /\ NoNativeWork
CheckFence == (~checked /\ checked') => (Mutable /\ deadlineOK /\ staged \in Admits)
AttemptFence == (pending="none" /\ pending' \in Admits) => (checked /\ Mutable /\ pending'=staged)
OutcomeFence == (phase="none" /\ phase'="reserved") => (checked /\ pending \in Admits /\ owner /\ ~unknown)
KeyFence == phase="cancelled" => phase'="cancelled"
CancelFence == fence => fence'
UnknownFence == unknown /\ owner => UNCHANGED Durable
ProtocolStep == CheckFence /\ AttemptFence /\ OutcomeFence /\ KeyFence /\ CancelFence /\ UnknownFence
AdmissionCheckFence == [][CheckFence]_vars
AdmissionAttemptFence == [][AttemptFence]_vars
AdmissionOutcomeFence == [][OutcomeFence]_vars
NoKeyReplay == [][KeyFence]_vars
NoFenceRevival == [][CancelFence]_vars
NoUnknownMutation == [][UnknownFence]_vars
(* Negative control: latest observed validity may become false after CheckAdmit.
   Neither API entry nor successful outcome is another admission clock check. *)
PhysicalSourceAtDurability == [][(phase="none" /\ phase'="reserved") => deadlineOK]_vars
=============================================================================
