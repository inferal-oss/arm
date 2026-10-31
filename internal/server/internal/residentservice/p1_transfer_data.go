package residentservice

// DATA projection only. Withdrawal, attempts, partial bytes and full native
// write are distinct; none of these observations supplies ACK/Ready/closure.
type P1TransferData struct {
	Attempted, FullWrite, ACKObserved bool
	Bytes, Written                    int
	Writes                            uint32
	FirstError                        error
}

func projectP1TransferData(call *channelOperation) P1TransferData {
	if call == nil {
		return P1TransferData{}
	}
	return P1TransferData{Attempted: true, FullWrite: call.writeComplete && len(call.bytes) > 0 && call.transferred == len(call.bytes) && call.writes > 0, Bytes: len(call.bytes), Written: call.transferred, Writes: call.writes, FirstError: call.firstError}
}

// Reads THIS group's actual retained operation including cancelled/error/partial
// outcomes. No supplied record/boolean or new native/funding authority is used.
func (p *P1ServiceGroup) TransferOriginalP1Data() (P1TransferData, error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return P1TransferData{}, ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.parent != p.parent {
		return P1TransferData{}, ErrInvalid
	}
	return projectP1TransferData(o.channel), nil
}
