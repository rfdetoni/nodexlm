// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';

void main() => runApp(const NodexApp());

class NodexApp extends StatelessWidget {
  const NodexApp({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
        title: 'NodexLM',
        debugShowCheckedModeBanner: false,
        theme: ThemeData(
          useMaterial3: true,
          brightness: Brightness.dark,
          colorSchemeSeed: const Color(0xff8eacdd),
          scaffoldBackgroundColor: const Color(0xff10141a),
          cardTheme: CardThemeData(
            color: const Color(0xff1a2029),
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
          ),
        ),
        home: const ControlCenter(),
      );
}

class ControlCenter extends StatefulWidget {
  const ControlCenter({super.key});
  @override
  State<ControlCenter> createState() => _ControlCenterState();
}

class _ControlCenterState extends State<ControlCenter> {
  final url = TextEditingController(text: 'https://127.0.0.1:8443');
  final token = TextEditingController();
  final caFile = TextEditingController();
  final model = TextEditingController();
  final prompt = TextEditingController();
  final pairCode = TextEditingController();
  final pairEndpoint = TextEditingController();
  final pairName = TextEditingController();
  String shownHostCode = "";
  final chats = <({String role, String text})>[];
  final nodes = <Map<String, dynamic>>[];
  bool busy = false;
  int page = 0;
  String status = 'Configure the coordinator connection to begin.';

  @override
  void dispose() {
    url.dispose();token.dispose();caFile.dispose();model.dispose();prompt.dispose();pairCode.dispose();pairEndpoint.dispose();pairName.dispose();super.dispose();
  }

  Future<dynamic> request(String method, String endpoint, [Object? body]) async {
    final base = Uri.tryParse(url.text.trim());
    if (base == null || base.scheme != 'https') {
      throw const FormatException('A valid HTTPS coordinator URL is required');
    }
    final context = SecurityContext(withTrustedRoots: true);
    if (caFile.text.trim().isNotEmpty) context.setTrustedCertificates(caFile.text.trim());
    final client = HttpClient(context: context)..connectionTimeout = const Duration(seconds: 8);
    try {
      final req = await client.openUrl(method, base.resolve(endpoint))
          .timeout(const Duration(seconds: 10));
      req.headers.set(HttpHeaders.authorizationHeader, 'Bearer ${token.text.trim()}');
      req.headers.contentType = ContentType.json;
      if (body != null) req.write(jsonEncode(body));
      final res = await req.close().timeout(const Duration(minutes: 5));
      final text = await res.transform(utf8.decoder).join();
      if (res.statusCode >= 300) throw StateError('HTTP ${res.statusCode}: $text');
      return text.isEmpty ? null : jsonDecode(text);
    } finally {
      client.close(force: true);
    }
  }

  Future<void> refreshNodes() async {
    setState(() { busy=true;status='Refreshing cluster…'; });
    try {
      final list=await request('GET','/v1/nodes') as List<dynamic>;
      setState(() {
        nodes..clear()..addAll(list.cast<Map<String,dynamic>>());
        status='${nodes.length} registered nodes';
      });
    } catch(e) {setState(() => status='Could not refresh: $e');}
    finally {if(mounted)setState(() =>busy=false);}
  }

  Future<void> sendMessage() async {
    final value=prompt.text.trim();
    if(value.isEmpty || busy || model.text.trim().isEmpty)return;
    prompt.clear();
    setState(() {busy=true;chats.add((role:'user',text:value));status='Running inference…';});
    try {
      final output=await request('POST','/v1/chat/completions',{
        'model': model.text.trim(),
        'messages':[
          for(final item in chats) {'role':item.role,'content':item.text},
        ],
        'max_tokens':512,
        'stream':false,
      }) as Map<String,dynamic>;
      final choices=output['choices'] as List<dynamic>;
      final response=(choices.first as Map<String,dynamic>)['message'] as Map<String,dynamic>;
      setState(() {chats.add((role:'assistant',text:'${response['content']}'));status='Complete';});
    } catch(e){setState(() {chats.add((role:'error',text:'$e'));status='Failed';});}
    finally {if(mounted)setState(() => busy=false);}
  }

  Widget infoPanel(String heading,String description,IconData icon) => Center(
    child: ConstrainedBox(
      constraints:const BoxConstraints(maxWidth:560),
      child:Card(child:Padding(padding:const EdgeInsets.all(24),child:Column(
        mainAxisSize:MainAxisSize.min,
        children:[Icon(icon,size:42),const SizedBox(height:16),Text(heading,style:Theme.of(context).textTheme.titleLarge),
          const SizedBox(height:10),Text(description,textAlign:TextAlign.center)]))),
    ),
  );

  Widget connection() => ListView(children:[
    const Text('Coordinator connection',style:TextStyle(fontSize:22,fontWeight:FontWeight.w600)),
    const SizedBox(height:12),
    TextField(controller:url,decoration:const InputDecoration(labelText:'HTTPS URL',border:OutlineInputBorder())),
    const SizedBox(height:12),
    TextField(controller:token,obscureText:true,decoration:const InputDecoration(labelText:'Admin token',border:OutlineInputBorder())),
    const SizedBox(height:12),
    TextField(controller:caFile,decoration:const InputDecoration(labelText:'Optional trusted certificate file',border:OutlineInputBorder())),
    const SizedBox(height:12),
    FilledButton.icon(onPressed:busy?null:refreshNodes,icon:const Icon(Icons.wifi_tethering),label:const Text('Connect')),
    const SizedBox(height:20),
    const Text('This UI does not load models into RAM. Close it while leaving headless nodes running.'),
  ]);

  String cliBinary() {
    final suffix = Platform.isWindows ? '.exe' : '';
    final bundled = File('${File(Platform.resolvedExecutable).parent.path}${Platform.pathSeparator}nodexlm$suffix');
    return bundled.existsSync() ? bundled.path : 'nodexlm';
  }

  Future<void> pairWithHost() async {
    final code = pairCode.text.trim().toUpperCase().replaceAll('-', '').replaceAll(' ', '');
    if (!RegExp(r'^[A-HJ-NP-Z2-9]{8}$').hasMatch(code)) {
      setState(() => status = "Enter the host's 8-character alphanumeric code.");
      return;
    }
    setState(() { busy = true; status = 'Finding host on the local network…'; });
    try {
      final args = ['join', '--code', '-'];
      if (pairEndpoint.text.trim().isNotEmpty) args.addAll(['--server', pairEndpoint.text.trim()]);
      if (pairName.text.trim().isNotEmpty) args.addAll(['--name', pairName.text.trim()]);
      final child = await Process.start(cliBinary(), args);
      child.stdin.writeln(code);
      await child.stdin.close();
      final output = child.stdout.transform(utf8.decoder).join();
      final errors = child.stderr.transform(utf8.decoder).join();
      final exitCode = await child.exitCode.timeout(const Duration(seconds: 22));
      final resultText = exitCode == 0 ? await output : await errors;
      if (!mounted) return;
      setState(() {
        status = resultText.trim();
        if (exitCode == 0) pairCode.clear();
      });
    } catch (e) { if (mounted) setState(() => status = 'Pairing failed: $e'); }
    finally { if (mounted) setState(() => busy = false); }
  }

  Future<void> showHostCode() async {
    setState(() { busy = true; status = 'Creating a new pairing window…'; });
    try {
      final outcome = await Process.run(cliBinary(), ['pairing']).timeout(const Duration(seconds: 12));
      if (!mounted) return;
      final text = outcome.stdout.toString();
      final match = RegExp(r'CODE: ([A-Z2-9]{8})').firstMatch(text);
      setState(() {
        shownHostCode = outcome.exitCode == 0 ? (match?.group(1) ?? '') : '';
        status = outcome.exitCode == 0 ? 'Host code valid for 10 minutes.' : outcome.stderr.toString().trim();
      });
    } catch (e) { if (mounted) setState(() => status = 'Cannot open pairing: $e'); }
    finally { if (mounted) setState(() => busy = false); }
  }

  Widget pairingPanel() => Card(child: Padding(
    padding: const EdgeInsets.all(16),
    child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      const Text('Join a cluster', style: TextStyle(fontWeight: FontWeight.w600, fontSize: 16)),
      const SizedBox(height: 8),
      const Text('Enter the 8-character code displayed on the host. The host is discovered on your LAN.', style: TextStyle(color: Colors.white60)),
      const SizedBox(height: 12),
      Wrap(spacing: 10, runSpacing: 10, crossAxisAlignment: WrapCrossAlignment.center, children: [
        SizedBox(width: 170, child: TextField(controller: pairCode, textCapitalization: TextCapitalization.characters,
          maxLength: 8, decoration: const InputDecoration(labelText: 'Host code', counterText: '', border: OutlineInputBorder()))),
        SizedBox(width: 260, child: TextField(controller: pairEndpoint,
          decoration: const InputDecoration(labelText: 'Host HTTPS URL (optional)', isDense: true, border: OutlineInputBorder()))),
        FilledButton.icon(onPressed: busy ? null : pairWithHost, icon: const Icon(Icons.link), label: const Text('Join securely')),
        OutlinedButton.icon(onPressed: busy ? null : showHostCode, icon: const Icon(Icons.key), label: const Text('Generate host code')),
      ]),
      if (shownHostCode.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 12),
        child: SelectableText(shownHostCode, style: const TextStyle(fontSize: 28, fontWeight: FontWeight.bold, letterSpacing: 4))),
    ]),
  ));

  Widget cluster() => Column(crossAxisAlignment:CrossAxisAlignment.start,children:[
    pairingPanel(),
    const SizedBox(height: 12),
    Row(children:[const Text('Cluster',style:TextStyle(fontSize:22,fontWeight:FontWeight.bold)),const Spacer(),
      IconButton(tooltip:'Refresh',onPressed:busy?null:refreshNodes,icon:const Icon(Icons.refresh))]),
    Expanded(child:nodes.isEmpty?infoPanel('No nodes loaded','Connect to the coordinator and add a worker using nodexlm join.',Icons.hub_outlined):
      ListView.builder(itemCount:nodes.length,itemBuilder:(context,index){
        final n=nodes[index];
        final online=n['status']=='online';
        return Card(child:Padding(padding:const EdgeInsets.all(16),child:Row(children:[
          Icon(Icons.computer,color:online?const Color(0xff8adab2):Colors.grey,size:28),
          const SizedBox(width:14),Expanded(child:Column(crossAxisAlignment:CrossAxisAlignment.start,children:[
            Text('${n['name']}',style:const TextStyle(fontWeight:FontWeight.w600)),
            Text('${n['platform']} / ${n['architecture']} · ${n['cpu_count']} CPU threads',style:const TextStyle(color:Colors.white54)),
            Text(((n['models'] as List?)??[]).join(' · '),maxLines:1,overflow:TextOverflow.ellipsis),
          ])),
          Chip(label:Text(online?(n['busy']==true?'Busy':'Online'):'Offline')),
        ])));
      })),
  ]);

  Widget chat() => Column(children:[
    Row(children:[const Text('Chat',style:TextStyle(fontSize:22,fontWeight:FontWeight.bold)),const Spacer(),
      SizedBox(width:240,child:TextField(controller:model,decoration:const InputDecoration(labelText:'Model ID',isDense:true,border:OutlineInputBorder())))]),
    const SizedBox(height:10),
    Expanded(child:chats.isEmpty?infoPanel('Local-first inference','Use your own GGUF model through a NodexLM worker. No remote cloud provider is required.',Icons.psychology_alt_outlined):
      ListView.builder(itemCount:chats.length,itemBuilder:(context,index){
        final msg=chats[index];
        return Align(alignment:msg.role=='user'?Alignment.centerRight:Alignment.centerLeft,
          child:ConstrainedBox(constraints:const BoxConstraints(maxWidth:760),child:Card(
            color:msg.role=='user'?const Color(0xff243955):const Color(0xff1b222c),
            child:Padding(padding:const EdgeInsets.all(14),child:SelectableText(msg.text)))));
      })),
    const SizedBox(height:10),
    Row(children:[Expanded(child:TextField(controller:prompt,onSubmitted:(_)=>sendMessage(),
      decoration:const InputDecoration(hintText:'Message your local cluster',border:OutlineInputBorder()))),
      const SizedBox(width:8),FilledButton(onPressed:busy?null:sendMessage,child:const Icon(Icons.send))]),
  ]);

  @override
  Widget build(BuildContext context) => Scaffold(
    body:SafeArea(child:Row(children:[
      NavigationRail(selectedIndex:page,onDestinationSelected:(v){setState(()=>page=v);if(v==0&&nodes.isEmpty&&!busy&&token.text.isNotEmpty)refreshNodes();},
        labelType:NavigationRailLabelType.all,leading:const Padding(padding:EdgeInsets.symmetric(vertical:20),child:Icon(Icons.hub,size:30)),destinations:const [
        NavigationRailDestination(icon:Icon(Icons.hub_outlined),selectedIcon:Icon(Icons.hub),label:Text('Cluster')),
        NavigationRailDestination(icon:Icon(Icons.chat_bubble_outline),selectedIcon:Icon(Icons.chat_bubble),label:Text('Chat')),
        NavigationRailDestination(icon:Icon(Icons.settings_outlined),selectedIcon:Icon(Icons.settings),label:Text('Settings')),
      ]),
      const VerticalDivider(width:1),
      Expanded(child:Padding(padding:const EdgeInsets.all(22),child:Column(children:[
        Expanded(child:page==0?cluster():page==1?chat():connection()),
        const Divider(),Row(children:[Icon(busy?Icons.sync:Icons.circle,size:12,color:busy?Colors.orange:Colors.green),
          const SizedBox(width:8),Expanded(child:Text(status,overflow:TextOverflow.ellipsis,style:const TextStyle(color:Colors.white54,fontSize:12)))]),
      ]))),
    ])),
  );
}
