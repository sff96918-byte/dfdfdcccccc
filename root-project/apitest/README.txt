apitest — standalone API chat tester (NOT connected to the bot code)
====================================================================
1. double-click apitest.bat          -> creates apitest.config.txt on first run
2. open apitest.config.txt, paste API_KEY (and BASE_URL / MODEL if not Featherless)
3. apitest.bat ping                  -> one raw request: shows HTTP status + JSON  (API works?)
4. apitest.bat                       -> chat: you type as the stranger, "eva>" is the AI
   /new  /state  /system  /prompt  /history  /quit
5. edit persona.txt to change how she talks, then /new
6. apitest.bat scenario horny        -> plays the scripted stranger from scenarios.txt
   apitest.bat scenario all          -> all scenarios (horny, warm, cold, asl, tricky, noeng, money)
   apitest.bat compare warm          -> same scenario on every COMPARE_MODELS model, side-by-side table
   every chat is appended to apitest.log (send that file for review)
7. HANDOFF.md = where we are / what is next (read first after a break). PLAN.md explains the funnel "director": interest score -> HOT/WARM/COLD ->
   snap shared at reply 4 / 6 / 8 (SHARE_* in config), moods, double lines.
   Set SNAP_USERNAME in apitest.config.txt to see the real share line.

Each reply line shows: seconds taken, tokens, persona, and a WARN if the
output would fail the bot's rules (too long, @/link, <think> reasoning).
Needs Go (install.bat of the main project installs it). Builds apitest.exe here.
