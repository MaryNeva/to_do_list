#!/usr/bin/env python3
"""Isolated 1,000-user API load demo. Python standard library, Docker and Go required."""
import argparse,base64,collections,concurrent.futures,datetime,json,math,os,pathlib,random,secrets,statistics,subprocess,sys,threading,time,urllib.request,urllib.error,urllib.parse
P=argparse.ArgumentParser(description=__doc__)
P.add_argument('--users',type=int,default=1000)
P.add_argument('--concurrency',type=int,default=20)
P.add_argument('--duration',type=int,default=120,help='Mixed-workload duration in seconds, after all users log in')
P.add_argument('--login-workers',type=int,default=4)
P.add_argument('--think-time',type=float,default=.25,help='Pause per worker between operations')
P.add_argument('--project',default='todo-loadtest')
P.add_argument('--api-port',type=int,default=8081)
P.add_argument('--prometheus-port',type=int,default=9091)
P.add_argument('--grafana-port',type=int,default=3001)
P.add_argument('--prepare-only',action='store_true',help='Seed and authenticate users; save owner-only sessions for the async burst runner')
P.add_argument('--down',action='store_true',help='Stop the isolated stack; preserve its volumes')
P.add_argument('--output',default='/tmp/todo-loadtest')
a=P.parse_args()
if not (1<=a.users<=10000 and 1<=a.concurrency<=500 and 1<=a.login_workers<=16 and 1<=a.duration<=3600 and a.think_time>=0):P.error('Invalid size, duration or concurrency')
if not a.project.startswith('todo-loadtest') or any(c not in 'abcdefghijklmnopqrstuvwxyz0123456789-' for c in a.project):P.error('project must start with todo-loadtest and contain lowercase letters, digits or hyphens')
# Locate repository from installed script, or explicit environment during development.
ROOT=pathlib.Path(os.getenv('TODO_PROJECT_ROOT',pathlib.Path(__file__).resolve().parents[1]))
OUT=pathlib.Path(a.output).resolve();OUT.mkdir(parents=True,exist_ok=True);os.chmod(OUT,0o700)
COMPOSE=OUT/'compose.json';STATE=OUT/'private-state.json'
def run(cmd,**kw):return subprocess.run(cmd,check=True,**kw)
def docker(*args,**kw):return run(['docker','compose','-p',a.project,'-f',str(COMPOSE),*args],**kw)
if a.down:
 if not COMPOSE.exists():P.error('No compose file found in output directory')
 docker('down');sys.exit(0)
if STATE.exists():
 state=json.loads(STATE.read_text())
 if state['project']!=a.project:P.error('output directory belongs to another project')
else:
 state={'project':a.project,'db_password':secrets.token_urlsafe(24),'jwt':secrets.token_urlsafe(40),'grafana_password':secrets.token_urlsafe(24)}
 fd=os.open(STATE,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
 with os.fdopen(fd,'w') as f:json.dump(state,f)
def mount(src,dst):return str(ROOT/src)+':'+dst+':ro'
services={
 'db':{'image':'postgres:17-alpine','environment':{'POSTGRES_USER':'loadtest','POSTGRES_PASSWORD':state['db_password'],'POSTGRES_DB':'todo_loadtest'},'volumes':['dbdata:/var/lib/postgresql/data'],'healthcheck':{'test':['CMD-SHELL','pg_isready -U loadtest -d todo_loadtest'],'interval':'2s','timeout':'3s','retries':30}},
 'app':{'image':'to-do-list-app:local','depends_on':{'db':{'condition':'service_healthy'}},'environment':{'DB_HOST':'db','DB_PORT':'5432','DB_NAME':'todo_loadtest','DB_USER':'loadtest','DB_PASSWORD':state['db_password'],'JWT_SECRET':state['jwt'],'APP_ENV':'production','ADMIN_USERNAME':'','ADMIN_PASSWORD_HASH':'','METRICS_ADDRESS':':9101','METRICS_ENABLED':'true','RATELIMIT_AUTH_MAX_REQUESTS':'100000','RATELIMIT_AUTH_WINDOW':'1m'},'ports':[f'127.0.0.1:{a.api_port}:8080'],'healthcheck':{'test':['CMD-SHELL','wget -qO- http://127.0.0.1:8080/readyz >/dev/null'],'interval':'2s','timeout':'3s','retries':60}},
 'prometheus':{'image':'prom/prometheus:v2.55.1','command':['--config.file=/etc/prometheus/prometheus.yml','--storage.tsdb.path=/prometheus','--storage.tsdb.retention.time=2d'],'volumes':[mount(pathlib.Path('deployments/prometheus/prometheus.yml'),'/etc/prometheus/prometheus.yml'),'promdata:/prometheus'],'ports':[f'127.0.0.1:{a.prometheus_port}:9090']},
 'grafana':{'image':'grafana/grafana:11.3.0','environment':{'GF_SECURITY_ADMIN_USER':'admin','GF_SECURITY_ADMIN_PASSWORD':state['grafana_password'],'GF_USERS_ALLOW_SIGN_UP':'false'},'volumes':[mount(pathlib.Path('deployments/grafana/provisioning'),'/etc/grafana/provisioning'),mount(pathlib.Path('deployments/grafana/dashboards'),'/var/lib/grafana/dashboards'),'grafdata:/var/lib/grafana'],'ports':[f'127.0.0.1:{a.grafana_port}:3000']}}
fd=os.open(COMPOSE,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
with os.fdopen(fd,'w') as f:json.dump({'services':services,'volumes':{x:{} for x in ['dbdata','promdata','grafdata']}},f)
print('Starting isolated stack; normal application database is untouched.',flush=True)
docker('up','-d','--wait','--wait-timeout','120')
BASE=f'http://127.0.0.1:{a.api_port}'
lock=threading.Lock();records=[];phase='setup';runid=secrets.token_hex(5);password=secrets.token_urlsafe(24)
report={'run_id':runid,'users':a.users,'concurrency':a.concurrency,'duration_seconds':a.duration,'notes':['Seeded users share one bcrypt hash of a random run-specific password; seeding is not a registration benchmark.','All users authenticate through the real login endpoint. Bcrypt cost is unchanged.','Auth limiter raised only in isolated stack; this is not a rate-limiter test.','Closed-loop workload with think time; not an open-loop arrival-rate capacity test.','Infrastructure shares the host with other applications.']}
def req(method,path,body=None,token=None,expected=200,record=True):
 h={'Content-Type':'application/json'}
 if token:h['Authorization']='Bearer '+token
 r=urllib.request.Request(BASE+path,headers=h,method=method,data=json.dumps(body).encode() if body is not None else None)
 start=time.perf_counter();code=0;data={}
 try:
  with urllib.request.urlopen(r,timeout=20) as response:code=response.status;raw=response.read()
  data=json.loads(raw) if raw else {}
 except urllib.error.HTTPError as e:code=e.code;e.read()
 except (OSError,ValueError):code=0
 if record:
  with lock:records.append({'phase':phase,'operation':method+' '+('/api/v1/tasks/:id' if path.startswith('/api/v1/tasks/') and path.rsplit('/',1)[-1].isdigit() else path.split('?')[0]),'status':code,'expected':expected,'ms':(time.perf_counter()-start)*1000})
 return code,data
startms=int(time.time()*1000)
try:
 # Generate a real bcrypt hash once, using the repository's configured cost.
 r=run(['go','run','./cmd/hashpw'],cwd=ROOT,input=password+'\n',text=True,capture_output=True)
 hashvalue=r.stdout.strip().splitlines()[-1]
 if not hashvalue.startswith('$2'):raise RuntimeError('Password hash generation failed')
 # All interpolated SQL values below are internally generated hex, bcrypt or integers.
 prefix='load_'+runid+'_'
 sql=f"INSERT INTO users(username,email,password_hash) SELECT '{prefix}'||n, '{prefix}'||n||'@example.com', '{hashvalue}' FROM generate_series(1,{a.users}) n; INSERT INTO tasks(title,description,creator_id) SELECT 'Load seed task', 'Isolated load test',id FROM users WHERE username LIKE '{prefix}%'; SELECT count(*) FROM users WHERE username LIKE '{prefix}%';"
 seeded=docker('exec','-T','db','psql','-v','ON_ERROR_STOP=1','-U','loadtest','-d','todo_loadtest','-tA',input=sql,text=True,capture_output=True)
 if seeded.stdout.strip().splitlines()[-1]!=str(a.users):raise RuntimeError('Seed count mismatch')
 report['seeded_users']=a.users
 print(f'Seeded {a.users} users and one initial task per user. Waiting for baseline scrape.',flush=True);time.sleep(30)
 phase='login';sessions=[];login_start=time.time()
 def login(n):
  code,data=req('POST','/api/v1/auth/login',{'username':prefix+str(n),'password':password})
  if code!=200:return None
  return {'username':prefix+str(n),'token':data['token'],'refresh':data['refresh_token'],'lock':threading.Lock()}
 with concurrent.futures.ThreadPoolExecutor(max_workers=a.login_workers) as pool:
  for i,s in enumerate(pool.map(login,range(1,a.users+1)),1):
   if s:sessions.append(s)
   if i%100==0 or i==a.users:print(f'Authenticated {i}/{a.users}; successful {len(sessions)}',flush=True)
 report['authenticated_users']=len(sessions);report['login_seconds']=round(time.time()-login_start,2)
 if len(sessions)!=a.users:raise RuntimeError('Not all seeded users authenticated; see report')
 if a.prepare_only:
  private={'base_url':BASE,'password':password,'sessions':[{k:v for k,v in s.items() if k!='lock'} for s in sessions]}
  fd=os.open(OUT/'prepared-sessions.json',os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
  with os.fdopen(fd,'w') as f:json.dump(private,f)
  report['prepared_only']=True
 else:
  phase='workload';stop=time.monotonic()+a.duration;visits=collections.Counter();cursor=0
  def worker(worker_id):
   global cursor
   rng=random.Random(worker_id)
   while time.monotonic()<stop:
    with lock:index=cursor%len(sessions);cursor+=1;visits[index]+=1
    s=sessions[index]
    with s['lock']:
     code,page=req('GET','/api/v1/tasks/?limit=10&offset=0',token=s['token'])
     roll=rng.random()
     if code==200:
      if roll<.15:
       code,pair=req('POST','/api/v1/auth/refresh',{'refresh_token':s['refresh']})
       if code==200:s['token']=pair['token'];s['refresh']=pair['refresh_token']
      elif roll<.35:
       code,task=req('POST','/api/v1/tasks/',{'title':'Load workload task','description':runid},s['token'],201)
       if code==201:
        tid=task['id'];req('PATCH',f'/api/v1/tasks/{tid}',{'status':'in_progress'},s['token']);req('DELETE',f'/api/v1/tasks/{tid}',token=s['token'],expected=204)
      elif roll<.7 and page.get('items'):
       task=page['items'][0];req('GET',f'/api/v1/tasks/{task["id"]}',token=s['token'])
      else:req('GET','/api/v1/auth/me',token=s['token'])
    time.sleep(a.think_time)
  print(f'Running mixed workload: {a.concurrency} concurrent clients for {a.duration}s...',flush=True)
  with concurrent.futures.ThreadPoolExecutor(max_workers=a.concurrency) as pool:list(pool.map(worker,range(a.concurrency)))
  report['active_users']=len(visits)
  phase='logout'
  with concurrent.futures.ThreadPoolExecutor(max_workers=a.login_workers) as pool:list(pool.map(lambda s:req('POST','/api/v1/auth/logout',{'refresh_token':s['refresh']},expected=204),sessions))
 report['completed']=True
except (Exception,KeyboardInterrupt) as e:
 report['completed']=False;report['error']=type(e).__name__+': '+str(e)
 print('Run failed:',report['error'],file=sys.stderr,flush=True)
finally:
 print('Waiting for final metrics scrape...',flush=True);time.sleep(30)
 endms=int(time.time()*1000)
 def summary(rows):
  values=sorted(r['ms'] for r in rows)
  return {'requests':len(rows),'unexpected':sum(r['status']!=r['expected'] for r in rows),'statuses':dict(collections.Counter(str(r['status']) for r in rows)),'p50_ms':round(values[max(0,math.ceil(.5*len(values))-1)],2) if values else None,'p95_ms':round(values[max(0,math.ceil(.95*len(values))-1)],2) if values else None,'p99_ms':round(values[max(0,math.ceil(.99*len(values))-1)],2) if values else None}
 report['summary']=summary(records);report['phases']={x:summary([r for r in records if r['phase']==x]) for x in sorted(set(r['phase'] for r in records))}
 report['operations']={x:summary([r for r in records if r['operation']==x]) for x in sorted(set(r['operation'] for r in records))}
 report['from_ms']=startms;report['to_ms']=endms
 report['dashboard_urls']=[f'http://localhost:{a.grafana_port}/d/{uid}?from={startms}&to={endms}' for uid in ['todo-api-demo','todo-auth-demo','todo-runtime-demo']]
 path=OUT/('report-'+runid+'.json');path.write_text(json.dumps(report,indent=2)+'\n')
 print(json.dumps(report['summary'],indent=2));print('Report:',path)
 print('Grafana user: admin; password stored only in',STATE)
 for url in report['dashboard_urls']:print(url)
 print('Isolated database is preserved for inspection. Stop with the same command plus --down.')
sys.exit(0 if report.get('completed') and not report['summary']['unexpected'] else 1)
