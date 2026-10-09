"""Safe cross-platform CC draft schema checks using synthetic records."""
from pathlib import Path
import json, subprocess, shutil, tempfile, re, os
from urllib.request import urlopen, Request
from urllib.error import HTTPError

SRC=Path(__file__).resolve().parents[1]/'app'
with tempfile.TemporaryDirectory() as t:
    base=Path(t)/'install'
    for s in ('App','Templates','Config','CommandCenter/Config','CommandCenter/App','CommandCenter/Scripts'):(base/s).mkdir(parents=True,exist_ok=True)
    shutil.copy2(SRC/'App/office.html',base/'App/office.html')
    exe=Path(t)/'OfficeManager-test-native'
    subprocess.run(['go','build','-o',str(exe),'main.go'],cwd=SRC,check=True)
    shutil.copy2(exe,base/'OfficeManager-test-native')
    for x in (SRC/'Templates').iterdir():shutil.copy2(x,base/'Templates'/x.name)
    (base/'CommandCenter/RUN_TOP_NOTCH_COMMAND_CENTER.bat').write_text('@echo off\n')
    (base/'CommandCenter/App/TopNotch_CommandCenter.ps1').write_text('# fixture\n')
    (base/'CommandCenter/Scripts/Launch.ps1').write_text('# fixture\n')
    root=Path(t)/'Real Root';(base/'Config/settings.json').write_text(json.dumps({'RootDirectory':str(Path(t)/'Wrong Root')}))
    (base/'CommandCenter/Config/settings.json').write_text(json.dumps({'RootDirectory':str(root)}))
    proc=subprocess.Popen([str(base/'OfficeManager-test-native'),'--no-browser'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        m=re.search(r':(\d+)/',proc.stdout.readline());assert m
        port=int(m.group(1));endpoint='http://127.0.0.1:'+str(port)
        def api(path,obj=None):
            req=Request(endpoint+path,headers={'Content-Type':'application/json'},data=json.dumps(obj).encode() if obj is not None else None,method='POST' if obj is not None else 'GET')
            try:
                with urlopen(req,timeout=10) as response:return response.status,json.load(response)
            except HTTPError as e:return e.code,json.load(e)
        code,state=api('/api/state');assert state['root']==str(root)
        assert state['integrations']['commandCenter']['installed']
        assert not state['integrations']['commandCenter']['setupMarkerPresent']
        code,data=api('/api/save',{'address':'100 QA Test Lane, Jacksonville','inspectionDateTime':'2026-10-08T13:45','clientName':'Synthetic Buyer','clientEmail':'qa@example.org','services':{'fourPoint':True,'windMit':True}})
        assert code==200,data
        id=data['job']['id']
        code,prep=api('/api/prepare',{'id':id});assert code==200,prep
        draft=Path(prep['workspace'])/'JobInfo.json'
        info=json.loads(draft.read_text())
        assert info['Services']['FourPoint'] and info['Services']['WindMit']
        assert not info['Services']['WDO']
        assert info['Buyer']['Name']=='Synthetic Buyer'
        assert not info['AddressVerification']['Verified']
        assert info['InspectionDateTime']=='2026-10-08T13:45:00'
        draft.write_text(json.dumps({'PreservedFromCC':True}))
        code,reprep=api('/api/prepare',{'id':id});assert code==200
        assert json.loads(draft.read_text())=={'PreservedFromCC':True},'CC JobInfo overwritten'
        code,err=api('/api/open',{'what':'cc','id':id});assert code==400 and 'Windows' in err['error']
        (base/'CommandCenter/Scripts/Launch.ps1').unlink()
        code,state=api('/api/state');assert not state['integrations']['commandCenter']['installed']
        print('PASS portable integration: path precedence, detection, draft schema, CC file preservation, platform guard')
    finally:
        proc.terminate()
        try:proc.communicate(timeout=8)
        except subprocess.TimeoutExpired:proc.kill()
