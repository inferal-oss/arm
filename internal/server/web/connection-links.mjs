// Capture connection fragments synchronously, before route cleanup. popstate and
// hashchange can both describe one same-document navigation; reading current
// location (not event.newURL) ensures the one-use ticket is consumed once.
export function connectionLink(hash) {
  const values=new URLSearchParams(hash.slice(1)).getAll('connect');
  if(!values.length)return null;
  if(values.length!==1||!/^[a-f0-9]{64}$/.test(values[0]))return {error:'Invalid dashboard connection link. Run arm dashboard for a fresh link.'};
  return {ticket:values[0]};
}

export function createConnectionLinkHandler({location,navigation,onConnect,onStored}) {
  function consume(initial) {
    const link=connectionLink(location.hash);
    navigation.restore(); // Sanitizes the current entry before any auth request.
    if(link)onConnect(link);
    else if(initial)onStored();
  }
  return {start:()=>consume(true),change:()=>consume(false)};
}

// A newer link wins even if an older exchange resolves after cancellation.
// This coordinator only authenticates and connects: it never submits mutations.
export function createDashboardAuthentication({exchange,readSession,writeSession,clearSession,onStart,onSession,onError,now=Date.now}) {
  let revision=0,controller;
  return {async start(link=null) {
    const current=++revision;
    controller?.abort();controller=new AbortController();
    onStart(Boolean(link));
    if(link)clearSession();
    try {
      if(link?.error)throw Error(link.error);
      const session=link?await exchange(link.ticket,controller.signal):readSession();
      if(current!==revision)return;
      if(!session||!/^[a-f0-9]{64}$/.test(session.token)||!Number.isFinite(Date.parse(session.expires_at))||now()>=Date.parse(session.expires_at))throw Error('Run arm dashboard to open an authenticated local console.');
      if(link)writeSession(session);
      await onSession(session);
    }catch(error){if(current===revision)onError(error);}
  }};
}
