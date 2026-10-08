"""Real Windows executable + real Chromium, isolated synthetic data only."""
import json, os, re, shutil, subprocess, tempfile, time
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT=Path(__file__).resolve().parents[1]
APP=ROOT/'app'
with tempfile.TemporaryDirectory(prefix='tn-office-browser-') as tmp:
    inst=Path(tmp)/'installed'; (inst/'App').mkdir(parents=True); (inst/'Config').mkdir();(inst/'Templates').mkdir()
    for name in ('App/office.html',):shutil.copy2(APP/'App'/'office.html',inst/name)
    for source in (APP/'Templates').iterdir():shutil.copy2(source,inst/'Templates'/source.name)
    shutil.copy2(APP/'OfficeManager.exe',inst/'OfficeManager.exe')
    work=Path(tmp)/'Inspection Pictures'
    (inst/'Config/settings.json').write_text(json.dumps({'RootDirectory':str(work)}))
    proc=subprocess.Popen([str(inst/'OfficeManager.exe'),'--no-browser'],cwd=str(inst),stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        line=proc.stdout.readline().strip(); match=re.search(r'http://127\.0\.0\.1:\d+/',line)
        assert match, f'Windows app startup failed: {line}'
        url=match.group(0)
        with sync_playwright() as pw:
            browser=pw.chromium.launch(headless=True, executable_path='/usr/bin/chromium' if os.name != 'nt' else None, args=['--no-sandbox','--disable-dev-shm-usage'] if os.name != 'nt' else []) 
            page=browser.new_page(viewport={'width':1366,'height':768},accept_downloads=True)
            errors=[];page.on('pageerror',lambda e:errors.append(str(e)))
            page.goto(url,wait_until='networkidle')
            assert page.get_by_text('TOP NOTCH').is_visible()
            assert page.locator('#mToday').inner_text()=='0'
            page.locator('nav button[data-view="job"]').click()
            page.locator('input[name="address"]').fill('555 QA Browser Road, Jacksonville, FL')
            page.locator('input[name="inspectionDateTime"]').fill('2026-10-08T09:30')
            page.locator('input[name="clientName"]').fill('Browser Test Client')
            page.locator('input[name="clientEmail"]').fill('qa@example.org')
            page.locator('input[name="price"]').fill('475')
            page.locator('input[name="fourPoint"]').check()
            page.locator('input[name="agreementSigned"]').check()
            page.locator('input[name="paymentComplete"]').check()
            page.locator('#jobForm button[type="submit"]').click()
            page.get_by_text('Job saved successfully.').wait_for()
            page.locator('#prepare').click()
            page.get_by_text('Workspace and BLANK form originals prepared.',exact=False).wait_for()
            assert (work/'Oct. 2026'/'555 QA Browser Road'/'Ancillary Insurance Inspections'/'4-Point.pdf').exists()
            page.screenshot(path=str(ROOT/'windows-browser-job.png'),full_page=True)
            page.reload(wait_until='networkidle')
            page.locator('nav button[data-view="today"]').click()
            assert page.get_by_text('555 QA Browser Road',exact=False).count()>=1
            page.screenshot(path=str(ROOT/'windows-browser-dashboard.png'),full_page=True)
            page.locator('nav button[data-view="activity"]').click()
            assert page.get_by_text('PREPARE').count()>=1
            for width,height in ((1920,1080),(1366,768),(1024,768),(768,900),(390,844)):
                page.set_viewport_size({'width':width,'height':height})
                page.locator('nav button[data-view="today"]').click()
                assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth + 5'),f'Horizontal overflow at {width}px'
                assert page.locator('#allJobs').is_visible()
                page.screenshot(path=str(ROOT/f'windows-screen-{width}.png'),full_page=True)
            assert not errors,f'JavaScript exceptions: {errors}'
            browser.close()
        print('PASS Windows executable + live Chromium + job create/save/prepare/reload/activity + 5 viewports')
    finally:
        proc.terminate()
        try:proc.communicate(timeout=10)
        except subprocess.TimeoutExpired:proc.kill()
