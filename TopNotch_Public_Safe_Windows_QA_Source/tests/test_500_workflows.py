import tempfile,shutil,json,subprocess,time,re,hashlib,os,sys
from pathlib import Path
from urllib.request import urlopen,Request
from urllib.error import HTTPError
BASE=Path(__file__).resolve().parents[1]/'app'
passed=0

def check(cond,why):
 global passed
 if not cond:raise AssertionError(why)
 passed+=1

def request(port,path,data=None,headers=None):
 url=f'http://127.0.0.1:{port}{path}'
 req=Request(url,method='POST' if data is not None else 'GET',data=json.dumps(data).encode() if data is not None else None,headers={'Content-Type':'application/json',**(headers or {})})
 try:
  with urlopen(req,timeout=15) as r:
   return r.status,json.loads(r.read())
 except HTTPError as e:return e.code,json.loads(e.read())

with tempfile.TemporaryDirectory(prefix='office-go-test-') as tmp:
 base=Path(tmp)/'installed'
 for path in ['App','Config','Templates']:(base/path).mkdir(parents=True)
 for name in ['App/office.html','Templates/4-Point.pdf','Templates/4-Point Picture Form.docx','Templates/Wind Mitigation.pdf','Templates/Wind Mitigation Photo Documentation.docx']:
  shutil.copy2(BASE/name,base/name)
 shutil.copy2(BASE/'OfficeManager.exe',base/'OfficeManager.exe')
 root=Path(tmp)/'Inspection Pictures';(base/'Config/settings.json').write_text(json.dumps({'RootDirectory':str(root)}))
 p=subprocess.Popen([str(base/'OfficeManager.exe'),'--no-browser'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 try:
  line=p.stdout.readline().strip();m=re.search(r':(\d+)/',line);check(bool(m),'No process startup URL: '+line)
  port=int(m.group(1));code,state=request(port,'/api/state');check(code==200 and state['jobs']==[],'Initial state not empty')
  ids=[]
  for i in range(500):
   services={'homeInspection':True,'fourPoint':i%2==0,'windMit':i%3==0,'iaq':i%5==0}
   j={'address':f'{3000+i} Go QA Avenue, Jacksonville, FL','inspectionDateTime':'2026-10-08T09:30','clientName':'QC client','clientEmail':'qc@example.org','agreementSigned':True,'paymentComplete':True,'price':'300.30','services':services}
   code,s=request(port,'/api/save',j);check(code==200,'Create job failed');job=s['job'];ids.append(job['id'])
   code,prep=request(port,'/api/prepare',{'id':job['id']});check(code==200,'Prepare failed:'+str(prep));w=Path(prep['workspace'])
   check((w/'JobInfo.json').exists(),'JobInfo missing');check((w/'.topnotch-job-id').read_text()==job['id'],'job marker wrong')
   check((w/'Ancillary Insurance Inspections'/'4-Point.pdf').exists()==services['fourPoint'],'4Point mismatch')
   check((w/'Ancillary Insurance Inspections'/'Wind Mitigation.pdf').exists()==services['windMit'],'Wind mismatch')
   code,state=request(port,'/api/state');loaded=next(x for x in state['jobs'] if x['id']==job['id'])
   check(loaded['status']=='Ready','status Ready failed')
   code,s=request(port,'/api/save',{**loaded,'notes':f'updated {i}'});check(code==200,'Edit failed:'+str(s))
   code,s=request(port,'/api/status',{'id':job['id'],'step':'inspected'});check(code==200 and s['job']['status']=='Report / Docs Pending','mark inspected failed')
   code,s=request(port,'/api/status',{'id':job['id'],'step':'delivered'});check(code==200 and s['job']['status']=='Delivered / Follow-Up','mark delivered failed')
  check(len(set(ids))==500,'UUID collision')
  print('PASS 500 native-server end-to-end job workflows, 5,000+ assertions')
  shared={'address':'123 Same Address Rd, Jacksonville','inspectionDateTime':'2026-10-08T12:00','services':{'fourPoint':True}}
  code,a=request(port,'/api/save',shared);code,b=request(port,'/api/save',shared)
  check(a['job']['id']!=b['job']['id'],'Duplicate address overwrote job')
  check(request(port,'/api/prepare',{'id':a['job']['id']})[0]==200,'First duplicate prep failed')
  code,other=request(port,'/api/prepare',{'id':b['job']['id']});check(code==400 and 'another job' in other['error'],'Duplicate workspace not blocked')
  code,bad=request(port,'/api/save',{**shared,'address':''});check(code==400,'Empty address accepted')
  code,bad=request(port,'/api/save',{**shared,'price':'NaN'});check(code==400,'NaN accepted')
  code,bad=request(port,'/api/save',{**shared,'inspectionDateTime':'bad'});check(code==400,'Bad date accepted')
  code,bad=request(port,'/api/save',{**shared,'services':{}});check(code==400,'Zero services accepted')
  code,bad=request(port,'/api/save',{**shared,'address':'../'});check(code==400,'Path traversal accepted')
  code,bad=request(port,'/api/status',{'id':b['job']['id'],'step':'delivered'});check(code==400,'Premature delivery accepted')
  code,bad=request(port,'/api/save',shared,headers={'Origin':'https://evil.test'});check(code==403,'Cross-site post accepted')
  code,bad=request(port,'/api/state',headers={'Host':'evil.test'});check(code==403,'Host header poisoning accepted')
  code,s=request(port,'/api/state');check(len(s['jobs'])==502,'Saved records missing')
  j=next(x for x in s['jobs'] if x['id']==a['job']['id'])
  code,s=request(port,'/api/save',{**j,'notes':'first update'});check(code==200,'update failed')
  code,s=request(port,'/api/save',{**j,'notes':'stale update'});check(code==400,'Stale revision accepted')
  code,s=request(port,'/api/save',{**j,'address':'999 Elsewhere Street'});check(code==400,'Prepared address modified')
  path=root/'Oct. 2026'/'123 Same Address Rd'/'Ancillary Insurance Inspections'/'4-Point.pdf'
  path.write_bytes(b'TEST CUSTOMER ALTERATION')
  code,s=request(port,'/api/prepare',{'id':a['job']['id']});check(code==200,'repeat prep failed')
  check(path.read_bytes()==b'TEST CUSTOMER ALTERATION','Customer-edited document overwritten')
  print('PASS endpoint validation, immutable job identity, stale revision protection, no-overwrite, local-only security')
  print('GO_BACKEND_ASSERTIONS_PASS=',passed)
 finally:
  p.terminate()
  try:p.communicate(timeout=5)
  except subprocess.TimeoutExpired:p.kill()
