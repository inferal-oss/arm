// Apple EncryptedInputEvent pointer body, before AES-128-ECB encryption.
// The 0xff marker and Apple button ordering are required by screensharingd.
export function buildArdPointerBlock(mask,x,y,width,height){
    const clamp=(value,limit)=>Math.max(0,Math.min(0xffff,Math.min(limit-1,Math.trunc(value)||0)));
    const block=new Uint8Array(16);
    block[10]=0xff;
    block[11]=(mask&~0x06)|((mask&0x04)>>1)|((mask&0x02)<<1);
    const view=new DataView(block.buffer);
    view.setUint16(12,clamp(x,width),false);
    view.setUint16(14,clamp(y,height),false);
    return block;
}
