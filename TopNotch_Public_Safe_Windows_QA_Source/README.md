# Top Notch Office Manager - Windows Test Lab (PUBLIC-SAFE)

**Testing only; NOT a finished or approved Office Manager.** This repository contains the Go application source, HTML interface, and test scripts. Actual insurance forms, customer data and credentials are deliberately excluded.

## What it does

A push to `main` triggers `.github/workflows/windows-acceptance.yml` on GitHub-hosted Windows Server 2025. CI builds a real Windows `OfficeManager.exe`, creates synthetic placeholder PDF/DOCX files, launches the executable and performs 500 API workflow cycles. It then launches Chromium on Windows and tests the live app's browser controls and five viewport sizes. Screenshots and the test executable are uploaded as temporary GitHub Actions artifacts.

## Add this project

From an empty repository's web page, use **Add file -> Upload files** and upload the extracted contents, preserving `.github/workflows/windows-acceptance.yml` as well as `app/` and `tests/`. GitHub Actions should start automatically after committing to `main` if Actions is enabled. If GitHub's web interface doesn't preserve folder structure, use GitHub Desktop to commit/push the extracted root directory instead.

## Scope limitations

This workflow confirms only the tested native server, job-record operations and synthetic document-copy paths. It does not verify real 4-point/wind forms, HomeGauge, Command Center, ISN, permits or ASCE; no production release can be approved based only on this test. Do not upload real customer information into a public repository.
