"""Native Windows smoke check: Office Manager -> Command Center launcher with safe synthetic stub.
No actual legacy scripts are executed; this tests launching and draft context handoff only.
"""
import json, os, re, shutil, subprocess, tempfile, time
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import HTTPError

ROOT=Path(__file__).resolve().parents[1]; APP=ROOT/'app'
assert os.name=='nt', 'Windows-only acceptance test'

def api(port, path, payload=None):
    kw={}
    if payload is not None:
        kw={'method':'POST','headers':{'Content-Type':'application/json'},'data':json.dumps(payload).encode()}
    req=Request(f'http://127.0.0.1:{port}{path}',**kw)
    try:
        with urlopen(req,timeout=10) as r: return r.status,json.loads(r.read())
    except HTTPError as e: return e.code,json.loads(e.read())

with tempfile.TemporaryDirectory(prefix='tn-bridge-windows-') as td:
    inst=Path(td)/'Installed Office Manager'
    for d in ('App','Templates','Config','CommandCenter/App','CommandCenter/Scripts','CommandCenter/Config'):(inst/d).mkdir(parents=True,exist_ok=True)
    shutil.copy2(APP/'OfficeManager.exe', inst/'OfficeManager.exe')
    shutil.copy2(APP/'App/office.html', inst/'App/office.html')
    for f in (APP/'Templates').iterdir(): shutil.copy2(f, inst/'Templates'/f.name)
    root=Path(td)/'Pictures Workspace'
    (inst/'Config/settings.json').write_text(json.dumps({'RootDirectory':'C:/Some Stale Path'}))
    (inst/'CommandCenter/Config/settings.json').write_text(json.dumps({'RootDirectory':str(root)}))
    # Stub launcher deliberately contains no proprietary forms/scripts or service credentials.
    (inst/'CommandCenter/App/TopNotch_CommandCenter.ps1').write_text('# Stub legacy app; do not run')
    (inst/'CommandCenter/Scripts/Launch.ps1').write_text('# Stub launcher; do not run')
    cc=inst/'CommandCenter'
    launcher=cc/'RUN_TOP_NOTCH_COMMAND_CENTER.bat'
    launcher.write_text('@echo off\n>"%~dp0bridge-launch.txt" echo launched\n>"%~dp0bridge-handoff.txt" echo %TOPNOTCH_CC_JOBINFO%\n')
    proc=subprocess.Popen([str(inst/'OfficeManager.exe'),'--no-browser'],cwd=inst,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        line=proc.stdout.readline().strip(); m=re.search(r'127\.0\.0\.1:(\d+)/',line)
        assert m, line
        port=int(m.group(1))
        code,state=api(port,'/api/state'); assert code==200
        assert state['root']==str(root), 'Legacy CC root must be single source of truth'
        assert state['integrations']['commandCenter']['installed']
        assert not state['integrations']['commandCenter']['setupMarkerPresent']
        code,out=api(port,'/api/open',{'what':'cc'}); assert code==200 and not out['jobHandoff'],out
        for _ in range(60):
            if (cc/'bridge-launch.txt').exists():break
            time.sleep(.1)
        assert (cc/'bridge-launch.txt').exists(),'Actual .bat was not launched by Windows'
        j={'address':'1442 Mock Integration Dr, Jacksonville, FL','inspectionDateTime':'2026-10-08T13:45','clientName':'Fake Client','clientEmail':'qa@example.test','price':'149.95','services':{'homeInspection':True,'fourPoint':True,'windMit':True}}
        code,out=api(port,'/api/save',j);assert code==200,out
        id=out['job']['id']
        code,out=api(port,'/api/open',{'what':'cc','id':id});assert code==400 and 'Prepare' in out['error'],out
        code,out=api(port,'/api/prepare',{'id':id});assert code==200,out
        jobinfo=Path(out['workspace'])/'JobInfo.json'
        data=json.loads(jobinfo.read_text())
        assert data['Services']['FourPoint'] and data['Services']['WindMit']
        assert data['AddressVerification']['Verified'] is False
        assert data['Buyer']['Name']=='Fake Client'
        code,out=api(port,'/api/open',{'what':'cc','id':id});assert code==200 and out['jobHandoff'],out
        handoff=cc/'bridge-handoff.txt'
        for _ in range(60):
            if handoff.exists() and str(jobinfo).casefold() in handoff.read_text().casefold():break
            time.sleep(.1)
        assert handoff.exists() and str(jobinfo).casefold() in handoff.read_text().casefold(), 'JobInfo handoff environment variable not inherited by stub .bat'
        assert api(port,'/api/open',{'what':'cc','id':'non-existent'})[0]==400
        # A missing script must prevent launch rather than claiming success.
        (cc/'Scripts/Launch.ps1').unlink()
        assert api(port,'/api/open',{'what':'cc'})[0]==400
        print('PASS native Windows CC bridge: install health, root sync, launch, JobInfo handoff, safety failures')
    finally:
        proc.terminate()
        try:proc.communicate(timeout=5)
        except subprocess.TimeoutExpired:proc.kill()
