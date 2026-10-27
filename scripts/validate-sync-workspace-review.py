from pathlib import Path
import base64,hashlib,json,os,platform,shutil,subprocess,time,zlib

EXPECTED_SOURCE_DIGEST = '733309cc3743cc132dd54038c06b9a516513c27c7dfcb0fbe71dcaa939140739'
EXPECTED_SOURCE_COUNT = 1841
ORIGINALS = {'workspace_evidence.go': {'sha256': '4115d450020c5bfdb3b00594a51ae2152900266714f9da4fd444e3481471cdf4', 'zlib_base64': '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'}, 'workspace_find.go': {'sha256': '1773932183f2ca5683ffab1d6e8da95121f79dc298ea8323e04411495f7ab78b', 'zlib_base64': 'eNqtV1tv2zYUfrZ+BWOghdSpyqUP7Vq4wOY4Q4Y03tJ23VAUBS3RNmeZ1EjKSdDkv+8ckrr4olzWPSQWycPv3Hg+HhY0XdAZI/papFMdBHxZSGVIGPT6qRSGXZk+fE6X9odL/C81/i+ome9Pec7wox/AzEzmVMwSqWb7V/v6Wu+Xgl/1gygIAEkbsqRXn6Ra6IKm7D2jKp2PhFGcaTIgrw5/POoUe0dNOrdiL46CYH+fnHCR1TKaaCsFH2bOyJzlGSkUX1HDSEEVE4ZMpSI0zwFaLVhGLuutCHbJzdzulIrPuKA5UUwzBfu5FAkZC1gpmLJDklJB5KUgS6kYbMIRrNeACQJ+ACyAKHNDuIYvmj2XIr+Gr1SumLombMUzJlIWEyENSXNGRVmAepCAb80ILc0crDHXFu9UpHJZ5MzABjrR6BDIItTznC+5qfEI7F75MMjSwC5GSrEQYHASTEuRbgQuTM0V8VlOhu43hmjmLDUsi6voPbuQEuYvKXxfNKGJSOi9/PzlWQ0aE6YU/kkVkW9Bj08JahkMiOA5ubmp4VtTXk8zgaqSFc15BkkMI7LnVgCup5gplcBhTEZKDSEFM5YFvVurCnW/HqDGBBbD6I2d6dgOSxv7KtuS4Zyli0dvd348avM0i6vdWC3JuGCCmtAjnRyHUUz6ST/2q18vjsfnZ3/d+NHx6cVo+GF8UU+cj0/GZ2fjT9V4eDYe/TkaxuQgqg29x6KMW2ukTs7Z5QnUd1hyYQqjwmkWVWci+Q2KPowANGNTpghsSoa51JCsoOdE3htqat80DMCZllsPtqfKyS68Ol87EeuThVuTY7bChTacnVuX+sgzlEKXXxyFEIRfoOxKngH8huQ7mbGnBwcvX6L8wX1nUwNx7PIAYrrD7D1Nl2w8+RssDdvRrFCi+9StqALSK0W2VptVtpAKQocB9PIT0WUKbKCnJVKfxTSKCg2iGskOfuYc+AmstPQFerIc2E9aokmBWJlKAAvc8IQwaFLaQ+79GjcUic4rtNQbaIV6X4HcGxatj1IPncG/WxylsMHg/oPAwn5zSjF2d9X89rGyqMxdPrFl6JHLCp7jCxgecxUeHr2KKq+cgMd8+rQ9w2UyGp9sK/IiXpkz/YcBULQIvWaP7pbe3nE9WmznJVzE6KVU07DfhNRdgIRdpYxlQK1PMuJ1vCZPLoE9OrHtyfno7oioO1Y+iQh63SSQte17SCJ2oFv4HvYQiFv1E8mvkotwjWu89uQcSsMWu1W5VwfBzuPeTY7yqvGe46JktU6skQnetvZ+wxkcxd4LJGKct5BRy8FOf9qpKQWmn07yVm+AvUNmbzTy5B+fl7ZLW6morbRti2qQ3tkxrrqVymbU+WldKESfLBpWGA7axbVFPA4v+QNKHrsdWDhsTbcu/47buTsivG5ivjciYPS2PWiEbVDuSHajrU2JIFzNV3E8FboA6q2Fws2WCH/xoKHGR5+N1PH0/xAG5BJLohF5O7ijb24ottbUPgWPpZalg+2kFq92y+zO6ncXwYDQAooucy7Fja2RJ6EWEw/WeXcC8wt/UQQNE31PO/efurn1nAyqvuCu+OILwPnPBZngBwTZBd0HeD2KO5vIB1yxXkYBpTXL0Bwgx31rbt6fLQU2Y+g2ZmsTv5cU9kJxsdsN3kfozmh1Jt+lzCHAWlDJ+UOAU7fbbz58WGHzobmY5ex54zm4ucROZf2hk5BTA2dnWmr35KPLCZ+V8L5649sXDdG3NQ94hJIlNDG8hdq8//RclvC+BCDUj1ipzPGU4aJVDqeWLBgrfLME1a6w2uUk5zOHgZeDlvkKDuaOV9l3P8o23mL+Hebj6bvP7Xfgtoqa3h7Wp2+d/cO2qJ3+fPDFZfShh7ajK7wNOsnVZq5oX7vavv5bT/odZXUb/AuINHvG'}}
CI_FILES={'.github/workflows/workspace-validation.yml','scripts/validate-sync-workspace-review.py'}
root=Path.cwd()
output=root/'.cache/workspace-ci'
output.mkdir(parents=True,exist_ok=True)
go=shutil.which('go')
if not go: raise SystemExit('Go executable unavailable')
env={**os.environ,'GOMAXPROCS':'1','GOFLAGS':'-p=1','GOTOOLCHAIN':'local'}
def hashes():
    names=subprocess.check_output(['git','ls-files','-z'],text=True).split('\0')
    return {n:hashlib.sha256(Path(n).read_bytes()).hexdigest() for n in sorted(names) if n}
original=hashes()
reviewed={n:d for n,d in original.items() if n not in CI_FILES}
assert len(reviewed)==EXPECTED_SOURCE_COUNT
assert hashlib.sha256(json.dumps(reviewed,sort_keys=True,separators=(',',':')).encode()).hexdigest()==EXPECTED_SOURCE_DIGEST
def verify():
    assert hashes()==original,'tracked source changed during native gate'
def receipt(name,args,expected_failure=False):
    verify()
    start=time.monotonic()
    log=output/(name+'.jsonl')
    with log.open('w') as stream:
        result=subprocess.run([go]+args,env=env,stdout=stream,stderr=subprocess.STDOUT,timeout=600)
    elapsed=time.monotonic()-start
    verify()
    record={'argv':[go]+args,'exit':result.returncode,'elapsed_seconds':elapsed,'sha256':hashlib.sha256(log.read_bytes()).hexdigest()}
    (output/(name+'-command.json')).write_text(json.dumps(record,indent=2)+'\n')
    if expected_failure:
        text=log.read_text()
        assert result.returncode!=0 and 'FIFO blocked marker inspection before type check' in text and 'malformed reserved sibling produced a complete-looking partial set' in text
    else:
        assert result.returncode==0,(name,result.returncode)
    print(name,record['exit'],round(elapsed,3),flush=True)

facts={'commit':subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip(),'platform':platform.platform(),'machine':platform.machine(),'uname':list(platform.uname()),'go':subprocess.check_output([go,'version'],text=True).strip(),'source_digest':EXPECTED_SOURCE_DIGEST,'source_count':len(reviewed),'tracked_sha256':original}
(output/'source-and-host.json').write_text(json.dumps(facts,indent=2)+'\n')
replace={}
for name,value in ORIGINALS.items():
    raw=zlib.decompress(base64.b64decode(value['zlib_base64']))
    assert hashlib.sha256(raw).hexdigest()==value['sha256']
    path=output/('original-'+name)
    path.write_bytes(raw)
    replace[str(root/'internal/syncfs'/name)]=str(path)
overlay=output/'original-overlay.json'
overlay.write_text(json.dumps({'Replace':replace})+'\n')
receipt('original-counterexamples',['test','-mod=readonly','-p=1','-race','-overlay',str(overlay),'-json','./internal/syncfs','-run','^(TestWorkspaceReviewMarkerFIFORefusesAndJoins|TestWorkspaceReviewMalformedSiblingKeepsResultUnknown)$','-count=1','-timeout=60s'],True)
receipt('final-syncfs-race',['test','-mod=readonly','-p=1','-race','-json','./internal/syncfs','-count=1','-timeout=180s'])
receipt('final-worker-boundary-race',['test','-mod=readonly','-p=1','-race','-json','./internal/syncworker','-run','^(TestFixedSyncWorkerOverARMSSHSession|TestFixedWorkerThroughDuplexTransfer|TestFailedTransportJoinDoesNotConfirmBaseline|TestConflictKeepBothOverSingleSSHStream|TestCancellationClosesBlockedSSHControl|TestUnreviewedWorkerCommandCannotDispatch)$','-count=1','-timeout=180s'])
receipt('final-vet',['vet','-mod=readonly','-p=1','./internal/syncfs','./internal/syncworker'])
verify()
(output/'closure.json').write_text(json.dumps({'source_unchanged':True,'commit':facts['commit'],'completed':True},indent=2)+'\n')
