// Presentation only: never feeds a Pod request, limit, review or selection.
// Kubernetes Quantity suffixes follow the pinned apimachinery resource parser.
const decimal = {n:-9,u:-6,m:-3,'':0,k:3,M:6,G:9,T:12,P:15,E:18};
const binary = {Ki:10,Mi:20,Gi:30,Ti:40,Pi:50,Ei:60};
function ratio(raw) {
  if(raw.length>256)return null;
  const match=/^\+?(?:(\d+)(?:\.(\d*))?|\.(\d+))([eE][+-]?\d+|[numkMGTPE]|[KMGTPE]i)?$/.exec(raw);
  if(!match)return null;
  const fraction=match[2]??match[3]??'',digits=(match[1]??'0')+fraction,suffix=match[4]??'';
  if(digits.length>128)return null;
  let numerator=BigInt(digits),denominator=10n**BigInt(fraction.length);
  if(Object.hasOwn(binary,suffix))numerator*=2n**BigInt(binary[suffix]);
  else {
    const exponent=Object.hasOwn(decimal,suffix)?decimal[suffix]:Number(suffix.slice(1));
    if(!Number.isInteger(exponent)||Math.abs(exponent)>30)return null;
    if(exponent>=0)numerator*=10n**BigInt(exponent);else denominator*=10n**BigInt(-exponent);
  }
  return {numerator,denominator};
}
function parts(whole,fraction='') {return whole.toLocaleString('en-US')+(fraction?'.'+fraction:'');}
function exactDecimal(numerator,denominator) {
  const whole=numerator/denominator;let remainder=numerator%denominator,fraction='';
  for(let i=0;remainder&&i<9;i++){remainder*=10n;fraction+=String(remainder/denominator);remainder%=denominator;}
  return remainder?null:parts(whole,fraction);
}
export function formatKubeQuantity(value,kind='bytes') {
  const raw=typeof value==='string'?value:typeof value==='number'||typeof value==='bigint'?String(value):'';
  if(!raw)return 'Not reported';
  const parsed=ratio(raw);if(!parsed)return raw;
  const {numerator,denominator}=parsed;
  if(kind==='cpu') {
    const display=exactDecimal(numerator,denominator);
    return display===null?raw:display+(numerator===denominator?' core':' cores');
  }
  if(kind!=='bytes')return raw;
  const units=['B','KiB','MiB','GiB','TiB','PiB','EiB'];let unit=0,divisor=denominator;
  while(unit<units.length-1&&numerator>=divisor*1024n){divisor*=1024n;unit++;}
  const places=unit?1:3,scale=10n**BigInt(places),scaled=numerator*scale;
  const rounded=(scaled*2n+divisor)/(divisor*2n);
  if(numerator&&rounded===0n)return raw;
  const fraction=String(rounded%scale).padStart(places,'0').replace(/0+$/,'');
  return (scaled%divisor?'≈ ':'')+parts(rounded/scale,fraction)+' '+units[unit];
}
