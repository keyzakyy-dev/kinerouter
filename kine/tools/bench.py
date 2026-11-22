import time, urllib.request, concurrent.futures, sys, subprocess, re, json
port = int(sys.argv[1]); label = sys.argv[2]
def one(_): urllib.request.urlopen(f'http://127.0.0.1:{port}/api/health', timeout=20).read()
for _ in range(100): one(0)          # fixed warmup
t0=time.time()
for _ in range(500): one(0)
serial=time.time()-t0
t0=time.time()
with concurrent.futures.ThreadPoolExecutor(32) as ex: list(ex.map(one, range(5000)))
par=time.time()-t0
print(json.dumps({"label":label,"serial_rps":round(500/serial),"par_rps":round(5000/par),
                  "serial_s":round(serial,3),"par_s":round(par,3)}))
