package residentbirth

type workerFooterIO struct {
	offset              int64
	requested, returned int
	returnedKnown       bool
	err                 error
}
type WorkerRootBoundFooterResultData struct {
	Footer                      WorkerRootBoundFooterData
	FooterBytes, JournalBytes   int
	FooterSHA256, JournalSHA256 string
}
