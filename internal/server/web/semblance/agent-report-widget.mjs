// The only foreign DOM is a presentation boundary. It has no output bridge,
// no sandbox permissions, no credentials, and no agent-provided destinations.
export function createAgentReportWidget(initial,{URL:URLs=globalThis.URL,Blob:BlobType=globalThis.Blob}={}) {
  let document=initial,frame=null,url=null;
  function clear(){if(frame)frame.removeAttribute('src');if(url)URLs.revokeObjectURL(url);url=null;}
  function render(){if(!frame)return;clear();if(!document)return;url=URLs.createObjectURL(new BlobType([document],{type:'text/html;charset=utf-8'}));frame.src=url;}
  return {
    widget:{mount(element){
      frame=element.ownerDocument.createElement('iframe');frame.setAttribute('sandbox','');frame.setAttribute('allow','');frame.setAttribute('referrerpolicy','no-referrer');frame.setAttribute('title','Agent status report · isolated static presentation');frame.setAttribute('width','100%');frame.setAttribute('height','480');
      element.append(frame);render();
      return {update(){},destroy(){clear();frame?.remove();frame=null;document='';}};
    }},
    update(value){document=typeof value==='string'?value:'';render();},
    clear(){document='';clear();},
  };
}
