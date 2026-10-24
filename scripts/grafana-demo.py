#!/usr/bin/env python3
"""Exercise a running local API; persist only redacted check results, never credentials."""
import argparse,datetime,getpass,json,os,pathlib,secrets,sys,time,urllib.request,urllib.error,urllib.parse
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--base-url',default='http://localhost:'+os.getenv('SERVER_PORT','8080'))
p.add_argument('--cycles',type=int,default=3)
p.add_argument('--pause',type=float,default=3)
p.add_argument('--scrape-wait',type=float,default=30)
p.add_argument('--admin',action='store_true',help='Prompt for admin credentials and verify read-only admin access')
p.add_argument('--report',default='grafana-demo-report.json')
a=p.parse_args()
if a.cycles<1 or a.pause<0 or a.scrape_wait<0:p.error('cycles >= 1 and delays >= 0 required')
u=urllib.parse.urlparse(a.base_url)
if u.scheme!='http' or u.hostname not in ('localhost','127.0.0.1','::1'):p.error('only local HTTP targets are supported')
a.base_url=a.base_url.rstrip('/')
results=[];users=[];start=int(time.time()*1000)
def call(method,path,expected,body=None,token=None,label=None):
 headers={'Content-Type':'application/json'}
 if token:headers['Authorization']='Bearer '+token
 req=urllib.request.Request(a.base_url+path,data=json.dumps(body).encode() if body is not None else None,headers=headers,method=method)
 t=time.monotonic()
 try:
  with urllib.request.urlopen(req,timeout=15) as r:status=r.status;raw=r.read()
 except urllib.error.HTTPError as e:status=e.code;raw=e.read()
 good=status==expected
 results.append({'scenario':label or method+' '+path,'status':status,'expected':expected,'passed':good,'duration_ms':round((time.monotonic()-t)*1000,2),'time':datetime.datetime.now(datetime.timezone.utc).isoformat()})
 print(('PASS' if good else 'FAIL'),label or method+' '+path,status,flush=True)
 if not good:raise RuntimeError('Unexpected HTTP status; response omitted to protect secrets')
 return json.loads(raw) if raw else {}
def check(condition,label):
 results.append({'scenario':label,'passed':bool(condition)})
 if not condition:raise RuntimeError(label)
 print('PASS',label,flush=True)
def auth(path,body,status=200):
 # Shared auth rate limit is 20/min by default. Stay below it without retrying rotations.
 time.sleep(max(4,a.pause))
 return call('POST','/api/v1/auth/'+path,status,body)
def login(user):
 r=auth('login',{'username':user['name'],'password':user['password']});user['token']=r['token'];return r
failed=False
try:
 call('GET','/readyz',200)
 print('Waiting for baseline Prometheus scrapes...',flush=True);time.sleep(a.scrape_wait)
 for cycle in range(a.cycles):
  name='demo_'+secrets.token_hex(6);password=secrets.token_urlsafe(24)
  user={'name':name,'password':password};reg=auth('register',{'username':name,'email':name+'@example.com','password':password},201)
  user['id']=reg['id'];users.append(user)
  session=login(user);token=user['token'];old=session['refresh_token']
  me=call('GET','/api/v1/auth/me',200,token=token);check(me['user_id']==user['id'] and not me['is_admin'],'User identity')
  auth('login',{'username':name,'password':'wrong-'+password},401)
  call('GET','/api/v1/tasks/',401)
  call('GET','/api/v1/users/',403,token=token)
  task=call('POST','/api/v1/tasks/',201,{'title':'Grafana demo task','description':'Isolated demo data'},token);tid=task['id']
  check(task['creator_id']==user['id'],'Task owner')
  for _ in range(5):
   page=call('GET','/api/v1/tasks/?limit=10&offset=0',200,token=token)
   check(any(x['id']==tid for x in page['items']),'Task appears in list');time.sleep(a.pause)
  call('GET','/api/v1/tasks/'+str(tid),200,token=token)
  edited=call('PATCH','/api/v1/tasks/'+str(tid),200,{'title':'Updated demo task'},token);check(edited['title']=='Updated demo task','Task update persisted')
  toggled=call('POST',f'/api/v1/tasks/{tid}/toggle-status',200,{},token);check(toggled['status']=='in_progress','Task status toggled')
  call('GET','/api/v1/tasks/?status=archived',400,token=token)
  call('POST','/api/v1/tasks/',400,{'title':''},token)
  new=auth('refresh',{'refresh_token':old});check(new['refresh_token']!=old,'Refresh token rotated')
  user['token']=new['token'];call('GET','/api/v1/auth/me',200,token=new['token'])
  auth('refresh',{'refresh_token':old},401)
  auth('refresh',{'refresh_token':new['refresh_token']},401)
  session=login(user)
  auth('logout',{'refresh_token':session['refresh_token']},204)
  auth('refresh',{'refresh_token':session['refresh_token']},401)
  session=login(user);newpassword=secrets.token_urlsafe(24)
  call('PATCH',f'/api/v1/users/{user["id"]}',200,{'password':newpassword},user['token']);user['password']=newpassword
  auth('refresh',{'refresh_token':session['refresh_token']},401)
  login(user)
  call('DELETE',f'/api/v1/tasks/{tid}',204,token=user['token'])
  call('GET',f'/api/v1/tasks/{tid}',404,token=user['token'])
  call('DELETE',f'/api/v1/users/{user["id"]}',204,token=user['token']);users.remove(user)
 if a.admin:
  name=input('Admin username: ');password=getpass.getpass('Admin password: ')
  session=auth('login',{'username':name,'password':password})
  me=call('GET','/api/v1/auth/me',200,token=session['token']);check(me['is_admin'],'Admin role')
  call('GET','/api/v1/users/?limit=1',200,token=session['token'])
except (Exception,KeyboardInterrupt) as e:
 failed=True;print('FAILED:',type(e).__name__,str(e),file=sys.stderr)
finally:
 for user in users:
  try:
   if not user.get('token'):login(user)
   call('DELETE',f'/api/v1/users/{user["id"]}',204,token=user['token'],label='Failure cleanup: own demo user')
  except Exception:failed=True;print('Cleanup incomplete for demo user id',user['id'],file=sys.stderr)
 print('Waiting for final Prometheus scrapes...',flush=True);time.sleep(a.scrape_wait)
 end=int(time.time()*1000)
 report={'started_ms':start,'finished_ms':end,'passed':not failed,'checks':results,'excluded':['server failures (5xx)','rate-limit exhaustion','token expiry','hourly janitor','database outages'],'dashboard_urls':['http://localhost:3000/d/'+uid+'?from='+str(start)+'&to='+str(end) for uid in ['todo-api-demo','todo-auth-demo','todo-runtime-demo']]}
 target=pathlib.Path(a.report);target.write_text(json.dumps(report,indent=2)+'\n')
 print('Report:',target.resolve())
 for link in report['dashboard_urls']:print(link)
sys.exit(1 if failed else 0)
