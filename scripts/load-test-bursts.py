#!/usr/bin/env python3
"""1,000 independent async virtual users, 15 minutes of activity waves, isolated local stack."""
import argparse,asyncio,collections,csv,datetime,json,math,os,pathlib,random,resource,subprocess,sys,time
try:import aiohttp
except ImportError:raise SystemExit('Missing aiohttp. Install it in a virtual environment: python3 -m pip install aiohttp')
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--users',type=int,default=1000)
p.add_argument('--duration',type=int,default=900,help='Workload seconds, excluding seeding/login/scrape waits')
p.add_argument('--output',default='/tmp/todo-loadtest')
p.add_argument('--seed',type=int,default=42)
a=p.parse_args()
if not 1<=a.users<=5000 or not 10<=a.duration<=3600:p.error('users: 1..5000; duration: 10..3600 seconds')
soft,hard=resource.getrlimit(resource.RLIMIT_NOFILE)
needed=a.users*2+256
if soft<needed:
 try:resource.setrlimit(resource.RLIMIT_NOFILE,(min(needed,hard),hard))
 except (ValueError,OSError):p.error(f'Increase the open file limit to at least {needed} before running')
 if resource.getrlimit(resource.RLIMIT_NOFILE)[0]<needed:p.error('Open file limit too low')
OUT=pathlib.Path(a.output).resolve();OUT.mkdir(parents=True,exist_ok=True)
ROOT=pathlib.Path(__file__).resolve().parent
print('Preparing isolated stack and authenticating every virtual user...',flush=True)
subprocess.run([sys.executable,str(ROOT/'load-test.py'),'--users',str(a.users),'--prepare-only','--output',str(OUT)],check=True)
privatefile=OUT/'prepared-sessions.json';private=json.loads(privatefile.read_text());sessions=private['sessions']
if len(sessions)!=a.users:raise SystemExit('Prepared session count mismatch')
BASE=private['base_url'];password=private['password']
# start/end in a 900-second run; mean think-time per action, not a promised RPS.
STAGES=[(0,120,'baseline',3.0),(120,180,'spike-1',.05),(180,300,'recovery-1',5.0),(300,420,'busy-period',.5),(420,480,'spike-2',0.0),(480,600,'recovery-2',6.0),(600,720,'high-activity',.25),(720,780,'spike-3',.02),(780,900,'cooldown',8.0)]
class Stats:
 def __init__(self):self.n=0;self.bad=0;self.codes=collections.Counter();self.hist=collections.Counter();self.ops=collections.Counter();self.timeouts=0
 def add(self,code,ms,expected,op):
  self.n+=1;self.bad+=code!=expected;self.codes[str(code)]+=1;self.hist[min(60000,math.ceil(ms))]+=1;self.ops[op]+=1
 def value(self):
  def quantile(q):
   total=0
   for ms,n in sorted(self.hist.items()):
    total+=n
    if total>=math.ceil(q*self.n):return ms
   return None
  return {'requests':self.n,'unexpected':self.bad,'statuses':dict(self.codes),'p50_ms':quantile(.5),'p95_ms':quantile(.95),'p99_ms':quantile(.99),'operations':dict(self.ops)}
stats=Stats();by_stage={s[2]:Stats() for s in STAGES};by_op=collections.defaultdict(Stats)
inflight=0;max_inflight=0;active=set();timeline=[];errors=collections.Counter();loop_lag_max=0;interrupted=False;workload_seconds=0;workload_end_ms=0
startmono=0;startms=0;deadline=0

def stage_at(elapsed):
 scaled=elapsed*900/a.duration
 for lo,hi,name,think in STAGES:
  if scaled<hi:return name,think
 return STAGES[-1][2:]
async def request(client,method,path,body=None,token=None,expected=200):
 global inflight,max_inflight
 if time.monotonic()>=deadline:return 0,{}
 op=method+' '+('/api/v1/tasks/:id'+('/toggle-status' if path.endswith('/toggle-status') else '') if path.startswith('/api/v1/tasks/') and path.split('/')[4:5] and path.split('/')[4].isdigit() else path.split('?')[0])
 name,_=stage_at(time.monotonic()-startmono);inflight+=1;max_inflight=max(max_inflight,inflight);t=time.perf_counter();code=0;data={}
 try:
  async with client.request(method,BASE+path,json=body,headers={'Authorization':'Bearer '+token} if token else None) as r:
   code=r.status
   raw=await r.read()
   if code<400 and raw:
    try:data=json.loads(raw)
    except ValueError:errors['invalid_json']+=1;code=0
 except asyncio.CancelledError:raise
 except (aiohttp.ClientError,asyncio.TimeoutError,OSError) as e:errors[type(e).__name__]+=1
 finally:inflight-=1
 ms=(time.perf_counter()-t)*1000;stats.add(code,ms,expected,op);by_stage[name].add(code,ms,expected,op);by_op[op].add(code,ms,expected,op)
 return code,data
async def vu(index,client):
 s=sessions[index];rng=random.Random(a.seed+index);task_id=None
 # Spread ordinary first requests, then use a barrier for the first synchronized spike.
 await asyncio.sleep(rng.uniform(0,min(5,a.duration/20)))
 while time.monotonic()<deadline:
  active.add(index);roll=rng.random()
  if roll<.45:
   code,page=await request(client,'GET','/api/v1/tasks/?limit=10&offset=0',token=s['token'])
   if code==200 and page.get('items'):task_id=page['items'][0]['id']
  elif roll<.6:
   await request(client,'GET',f'/api/v1/tasks/{task_id}' if task_id else '/api/v1/auth/me',token=s['token'])
  elif roll<.7:
   code,task=await request(client,'POST','/api/v1/tasks/',{'title':'Burst workload task','description':'Isolated async virtual user'},s['token'],201)
   if code==201:task_id=task['id']
  elif roll<.8 and task_id:
   await request(client,'PATCH',f'/api/v1/tasks/{task_id}',{'description':'Updated by async workload'},s['token'])
  elif roll<.86 and task_id:
   await request(client,'POST',f'/api/v1/tasks/{task_id}/toggle-status',{},s['token'])
  elif roll<.91 and task_id:
   code,_=await request(client,'DELETE',f'/api/v1/tasks/{task_id}',token=s['token'],expected=204)
   if code==204:task_id=None
  elif roll<.98:
   code,pair=await request(client,'POST','/api/v1/auth/refresh',{'refresh_token':s['refresh']})
   if code==200 and 'token' in pair and 'refresh_token' in pair:s['token']=pair['token'];s['refresh']=pair['refresh_token']
   elif code!=200:
    # Unknown refresh outcome: never retry an old token, since it may have been consumed.
    code,pair=await request(client,'POST','/api/v1/auth/login',{'username':s['username'],'password':password})
    if code==200:s['token']=pair['token'];s['refresh']=pair['refresh_token']
  else:await request(client,'GET','/api/v1/auth/me',token=s['token'])
  name,think=stage_at(time.monotonic()-startmono)
  pause=rng.uniform(.5,1.5)*think
  # Do not sleep past a stage boundary: all users wake for the first spike.
  boundaries=[lo*a.duration/900 for lo,hi,n,t in STAGES if lo*a.duration/900>time.monotonic()-startmono]
  if boundaries:pause=min(pause,max(0,boundaries[0]-(time.monotonic()-startmono)))
  await asyncio.sleep(min(pause,max(0,deadline-time.monotonic())))
async def monitor():
 global loop_lag_max
 previous=stats.n;tick=time.monotonic()
 while time.monotonic()<deadline:
  await asyncio.sleep(min(10,max(0,deadline-time.monotonic())))
  now=time.monotonic();elapsed=now-startmono;name,_=stage_at(elapsed);delta=now-tick
  lag=max(0,delta-10);loop_lag_max=max(loop_lag_max,lag)
  row={'elapsed_seconds':round(elapsed,2),'stage':name,'users_started':len(active),'requests':stats.n,'unexpected':stats.bad,'in_flight':inflight,'max_in_flight':max_inflight,'rps':round((stats.n-previous)/max(delta,.001),2),'monitor_lag_seconds':round(lag,3)}
  timeline.append(row);print(json.dumps(row),flush=True)
  with (OUT/'burst-progress.json').open('w') as f:json.dump(row,f)
  previous=stats.n;tick=now
async def main():
 global startmono,startms,deadline,interrupted,workload_seconds,workload_end_ms
 timeout=aiohttp.ClientTimeout(total=20,connect=10)
 connector=aiohttp.TCPConnector(limit=a.users,limit_per_host=a.users,ttl_dns_cache=300)
 async with aiohttp.ClientSession(timeout=timeout,connector=connector,trust_env=False) as client:
  startmono=time.monotonic();startms=int(time.time()*1000);deadline=startmono+a.duration
  jobs=[asyncio.create_task(vu(i,client)) for i in range(a.users)];watch=asyncio.create_task(monitor())
  try:await asyncio.gather(*jobs);await watch
  finally:
   for job in jobs+[watch]:
    if not job.done():job.cancel()
   await asyncio.gather(*jobs,watch,return_exceptions=True)
  workload_seconds=time.monotonic()-startmono;workload_end_ms=int(time.time()*1000)
  # Workload timing ends here; bounded logout is measured separately.
  sem=asyncio.Semaphore(10);logout_codes=collections.Counter()
  async def logout(s):
   async with sem:
    try:
     async with client.post(BASE+'/api/v1/auth/logout',json={'refresh_token':s['refresh']}) as r:await r.read();logout_codes[str(r.status)]+=1
    except (aiohttp.ClientError,asyncio.TimeoutError,OSError):logout_codes['0']+=1
  await asyncio.gather(*(logout(s) for s in sessions))
  return dict(logout_codes)
logout={};failure=None
try:logout=asyncio.run(main())
except (Exception,KeyboardInterrupt) as e:failure=type(e).__name__;print('Run interrupted/failed:',failure,file=sys.stderr)
endms=int(time.time()*1000)
report={'users':a.users,'users_started':len(active),'configured_duration_seconds':a.duration,'actual_workload_and_drain_seconds':round(workload_seconds or (time.monotonic()-startmono),2),'workload_end_ms':workload_end_ms,'max_requests_in_flight':max_inflight,'summary':stats.value(),'stages':{name:st.value() for name,st in by_stage.items()},'operations':{op:st.value() for op,st in by_op.items()},'transport_errors':dict(errors),'max_monitor_lag_seconds':loop_lag_max,'logout_statuses':logout,'failure':failure,'from_ms':startms,'to_ms':endms,'stage_plan':[{'start_seconds':lo*a.duration/900,'end_seconds':hi*a.duration/900,'name':name,'mean_think_seconds':think} for lo,hi,name,think in STAGES],'notes':['1000 independent user tasks, at most one in-flight request per user; 1000 simultaneous HTTP requests are not guaranteed.','Closed-loop workload: overload slows arrivals. Client shares the machine with the service.','Integer millisecond latency histogram; no unbounded raw request log.','Transport error during rotation triggers a real login, never a blind refresh replay.','Fixtures seeded directly; bcrypt login uses real API before timed workload.','Only isolated stack has increased auth limiter.'],'dashboard_urls':[f'http://localhost:3001/d/{uid}?from={startms}&to={endms}' for uid in ['todo-api-demo','todo-auth-demo','todo-runtime-demo']]}
path=OUT/f'burst-report-{startms}.json';path.write_text(json.dumps(report,indent=2)+'\n')
with (OUT/f'burst-timeline-{startms}.csv').open('w') as f:
 if timeline:
  writer=csv.DictWriter(f,fieldnames=timeline[0].keys());writer.writeheader();writer.writerows(timeline)
print('Waiting for final Prometheus scrape...',flush=True);time.sleep(30)
print('Summary:',json.dumps(report['summary']));print('Report:',path)
for url in report['dashboard_urls']:print(url)
print('Grafana login: admin. Password remains in',OUT/'private-state.json')
sys.exit(1 if failure or stats.bad or len(active)!=a.users or sum(n for code,n in logout.items() if code!='204') else 0)
