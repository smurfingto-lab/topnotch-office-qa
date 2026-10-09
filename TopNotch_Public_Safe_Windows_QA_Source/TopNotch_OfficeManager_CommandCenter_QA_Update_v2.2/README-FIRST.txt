TOP NOTCH - WINDOWS CI UPDATE, NOT A PRODUCTION RELEASE

This package contains ONLY public-safe source code and synthetic testing logic.
No actual customer records, production template files or private credentials are included.

The package updates files in your EXISTING GitHub repository.
Use GitHub Desktop to clone smurfingto-lab/topnotch-office-qa,
copy the CONTENTS of this extracted folder over the repository root,
replacing matching files, commit and push to main.

Do NOT upload the ZIP itself as one GitHub repository file.
Do NOT nest the whole patch inside an extra folder. The extracted
.github/workflows/windows-acceptance.yml must be at the repository root.

Expected workflow: 500 end-to-end test workflows, real Windows Command Center
bridge test with synthetic .bat, Chromium UI including launch and five sizes,
postrun screenshots and compiled EXE as evidence.

The real legacy Command Center, live ISN, ASCE, permits, and HomeGauge are
NOT uploaded to this public repository and NOT certified by these tests.
