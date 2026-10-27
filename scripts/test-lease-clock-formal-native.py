"""Conservative file closure for declared pinned macOS proof tool inputs.

Read load commands only; never run a candidate to discover its dependencies.
External images and the loader are byte-bound. Sealed system dyld-cache image
bytes remain an explicit operating-system boundary, with the OS build recorded.
"""
import hashlib
import os
from pathlib import Path
import platform
import plistlib
import struct
import sys


def require(value, message):
 if not value:raise ValueError(message)


def macho_images(path):
 """Read bounded thin/fat 64-bit Mach-O load commands, without execution."""
 size=path.stat().st_size
 def read(offset,count):
  require(0<=offset<=size and 0<=count<=1<<20 and offset+count<=size,'bounded Mach-O read required')
  with path.open('rb') as stream:stream.seek(offset);data=stream.read(count)
  require(len(data)==count,'short Mach-O input')
  return data
 magic=read(0,min(4,size))
 if magic==b'\xcf\xfa\xed\xfe':slices=[(0,size,None)]
 elif magic in (b'\xca\xfe\xba\xbe',b'\xca\xfe\xba\xbf'):
  count=struct.unpack('>I',read(4,4))[0];require(0<count<=16,'bounded Mach-O fat architecture roster required')
  width=20 if magic.endswith(b'\xbe') else 32;slices=[]
  for index in range(count):
   entry=read(8+index*width,width)
   values=struct.unpack('>iiIII' if width==20 else '>iiQQII',entry)
   offset,length=values[2:4];require(offset>=8+count*width and length>=32 and offset+length<=size,'invalid Mach-O fat slice')
   slices.append((offset,length,values[0]))
 else:return []
 images=[]
 for offset,length,declared_cpu in slices:
  header=struct.unpack('<IiiIIIII',read(offset,32))
  require(header[0]==0xfeedfacf and header[1] in (0x100000c,0x1000007),'supported little-endian 64-bit Mach-O required')
  require(declared_cpu is None or declared_cpu==header[1],'fat descriptor and Mach-O image architecture differ')
  count,bytes_count=header[4:6];require(count<=4096 and 8*count<=bytes_count<=1<<20 and 32+bytes_count<=length,'bounded Mach-O command table required')
  commands=read(offset+32,bytes_count);cursor=0;dependencies=[];rpaths=[]
  for _ in range(count):
   require(cursor+8<=len(commands),'truncated Mach-O command')
   command,width=struct.unpack_from('<II',commands,cursor)
   require(width>=8 and cursor+width<=len(commands),'invalid Mach-O command width')
   require(command!=0x27,'embedded LC_DYLD_ENVIRONMENT substitution refused')
   if command in (0xc,0x80000018,0x8000001f,0x80000023,0x20,0xe,0x8000001c):
    require(width>=12,'truncated Mach-O string command')
    start=struct.unpack_from('<I',commands,cursor+8)[0]
    require(12<=start<width,'invalid Mach-O string offset')
    value=commands[cursor+start:cursor+width]
    require(b'\0' in value,'unterminated Mach-O string')
    text=value.split(b'\0',1)[0].decode('utf-8');require(text and '\n' not in text,'invalid Mach-O dependency string')
    (rpaths if command==0x8000001c else dependencies).append(text)
   cursor+=width
  require(cursor==len(commands),'unparsed Mach-O load-command bytes')
  images.append({'architecture':'arm64' if header[1]==0x100000c else 'x86_64','kind':header[3],'dependencies':dependencies,'rpaths':rpaths})
 return images


class NativeProofClosure:
 def __init__(self,tool_closure,capture):
  self.closure=tool_closure;self.capture=capture;self.files=set();self.images=set();self.caches=set();self.records={};self.os_boundary={}
 def file(self,path):
  path=Path(path);require(path.is_absolute(),'absolute native dependency required')
  key=str(path)
  if key not in self.files:
   self.files.add(key);self.capture('proof-native-'+hashlib.sha256(key.encode()).hexdigest()[:20],path)
  return path.resolve(strict=True)
 def tree(self,path,label,pattern='**/*'):
  path=Path(path)
  self.closure.capture_roster(label,path,pattern)
  if path.is_dir():
   for item in sorted(path.glob(pattern)):
    require(not (item.is_symlink() and item.is_dir()),'native directory symlink requires explicit separate selection')
    if item.is_file():self.file(item)
 def system_cache(self,architecture):
  if architecture in self.caches:return
  self.caches.add(architecture)
  version=Path('/System/Library/CoreServices/SystemVersion.plist')
  self.file(version);data=plistlib.loads(version.read_bytes())
  require(isinstance(data,dict) and isinstance(data.get('ProductBuildVersion'),str) and isinstance(data.get('ProductVersion'),str),'exact macOS build metadata required')
  self.os_boundary.update(product_version=data['ProductVersion'],product_build=data['ProductBuildVersion'],metadata_source=str(version),excluded=['sealed system dyld-cache image bytes','kernel and Rosetta service state','generic OS command helpers/interpreters invoked by bundled scripts outside the declared proof pipeline'],cached_architectures=sorted(self.caches))
  if architecture=='x86_64' and platform.machine() in ('arm64','aarch64'):
   roots=(Path('/Library/Apple/usr/libexec/oah'),Path('/usr/libexec/rosetta'))
   require(any((root/'runtime').is_file() for root in roots),'pinned x86_64 proof backend requires installed Rosetta runtime')
   for index,root in enumerate(roots):self.tree(root,'proof-rosetta-'+str(index))
 def image(self,path,executable=None,inherited_rpaths=(),architecture=None):
  require(sys.platform=='darwin','native proof dependency closure is currently macOS only')
  resolved=self.file(path)
  parsed=macho_images(resolved)
  require(parsed,'selected native proof executable/library must be a real Mach-O image')
  if architecture is not None or len(parsed)>1:
   selected_arch=architecture or ('arm64' if platform.machine() in ('arm64','aarch64') else 'x86_64')
   parsed=[image for image in parsed if image['architecture']==selected_arch]
   require(len(parsed)==1,'exact compatible Mach-O architecture slice required')
  for image in parsed:
   executable=resolved if executable is None or image['kind']==2 else executable
   key=(str(resolved),image['architecture'],str(executable),tuple(inherited_rpaths))
   if key in self.images:continue
   self.images.add(key)
   require(len(self.images)<=512,'bounded native proof image closure required')
   def expand(value):
    if value.startswith('@loader_path/'):return str(resolved.parent/value[len('@loader_path/'):])
    if value.startswith('@executable_path/'):return str(executable.parent/value[len('@executable_path/'):])
    require(value.startswith('/'),'unsupported native loader selector')
    return value
   rpaths=tuple(expand(value) for value in image['rpaths'])+tuple(inherited_rpaths)
   self.records[key[0]+'#'+image['architecture']]=dict(image,executable=str(executable),expanded_rpaths=list(rpaths))
   for dependency in image['dependencies']:
    if dependency.startswith('@rpath/'):
     candidates=[Path(root)/dependency[len('@rpath/'):] for root in rpaths]
     # Bind absence of earlier candidates as well as the selected existing one.
     selected=None
     for index,candidate in enumerate(candidates):
      if candidate.exists():selected=candidate;break
      label='proof-rpath-absence-'+hashlib.sha256(str(candidate).encode()).hexdigest()[:20]
      if label not in self.closure.optional_receipts:self.closure.capture_optional(label,candidate)
     require(selected is not None,'unresolved native rpath dependency: '+dependency)
    else:selected=Path(expand(dependency))
    if selected.exists():self.image(selected,executable,rpaths,image['architecture'])
    else:
     require(str(selected).startswith(('/usr/lib/','/System/Library/')),'missing non-system native dependency: '+str(selected))
     label='proof-cache-image-absence-'+hashlib.sha256(str(selected).encode()).hexdigest()[:20]
     if label not in self.closure.optional_receipts:self.closure.capture_optional(label,selected)
     self.system_cache(image['architecture'])
     images=self.os_boundary.setdefault('cached_system_images',[])
     entry={'path':str(selected),'architecture':image['architecture']}
     if entry not in images:images.append(entry)
