# Online-first build

The D630 is only the client. Run the development environment in GitHub Codespaces and call Gemini over HTTPS.

1. Open this repository in Codespaces.
2. Set GEMINI_API_KEY as a Codespaces secret/environment variable.
3. Run: python -m pip install -e .
4. Run: agy
5. Use agy -p "..." for headless automation.
6. Let GitHub Actions run tests on hosted runners.

No local model or heavy build is required on the D630.
