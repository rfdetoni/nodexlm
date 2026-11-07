// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';

/// The desktop UI talks to the shared Go Hugging Face library through
/// `nodexlm hub`. No second downloader or separate model storage is needed.
class HubPanel extends StatefulWidget {
  const HubPanel({super.key, required this.binary, required this.onSelected,
    required this.coordinator, required this.caFile, required this.adminToken});
  final String binary;
  final String coordinator;
  final String caFile;
  final String adminToken;
  final ValueChanged<String> onSelected;

  @override
  State<HubPanel> createState() => _HubPanelState();
}

class _HubPanelState extends State<HubPanel> {
  final query = TextEditingController(text: 'Qwen GGUF');
  final modelsDir = TextEditingController(text: './models');
  final manualPath = TextEditingController();
  final matches = <Map<String, dynamic>>[];
  bool busy = false;
  bool localOnly = false;
  String info = 'Search public GGUF models on Hugging Face.';
  String progress = '';

  @override
  void dispose() {
    query.dispose(); modelsDir.dispose(); manualPath.dispose();
    super.dispose();
  }

  Future<void> search() async {
    if (busy || query.text.trim().length < 2) return;
    setState(() {busy = true; info = 'Searching Hugging Face…'; matches.clear();});
    try {
      final args = ['hub','search','--query',query.text.trim(),'--json'];
      if (localOnly) args.add('--fit');
      if (widget.adminToken.isNotEmpty && (widget.coordinator.startsWith('https://') || widget.coordinator.startsWith('http://'))) {
        args.addAll(['--server',widget.coordinator]);
        if (widget.caFile.isNotEmpty) args.addAll(['--ca',widget.caFile]);
      }
      final result = await Process.run(widget.binary, args, environment: {
        if (widget.adminToken.isNotEmpty) 'NODEXLM_ADMIN_TOKEN':widget.adminToken,
      }).timeout(const Duration(seconds: 35));
      if (result.exitCode != 0) throw StateError('${result.stderr}'.trim());
      final parsed = jsonDecode(result.stdout.toString()) as List<dynamic>;
      if (!mounted) return;
      setState(() {
        matches.addAll(parsed.map((e) => Map<String, dynamic>.from(e as Map)));
        info = '${matches.length} models found · Memory estimates, not guarantees';
      });
    } catch (e) {
      if (mounted) setState(() => info = 'Search failed: $e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> runModelCommand(List<String> args) async {
    if (busy) return;
    setState(() { busy = true; progress = ''; info = 'Preparing model…'; });
    Process? child;
    try {
      child = await Process.start(widget.binary, ['hub',...args]);
      final output = StringBuffer();
      child.stdout.transform(utf8.decoder).listen(output.write);
      child.stderr.transform(utf8.decoder).listen((text) {
        if (mounted) setState(() => progress = text.trim());
      });
      final code = await child.exitCode;
      if (code != 0) throw StateError(progress.isEmpty ? 'Command failed (exit $code)' : progress);
      final destination = output.toString().trim().split('\n').last;
      final basename = destination.split(RegExp(r'[/\\]')).last;
      if (!mounted) return;
      setState(() => info = 'Model available: $basename');
      widget.onSelected(basename);
    } catch (e) {
      if (mounted) setState(() => info = 'Could not import model: $e');
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Widget heading(String title, String subtitle, IconData icon) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 8),
    child: Row(children: [
      Icon(icon, size: 30, color: Theme.of(context).colorScheme.primary),
      const SizedBox(width: 12),
      Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Text(title, style: Theme.of(context).textTheme.headlineSmall),
        Text(subtitle, style: const TextStyle(color: Colors.white60)),
      ])),
    ]),
  );

  @override
  Widget build(BuildContext context) => Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
    heading('Model library', 'GGUF discovery, downloads and local imports', Icons.cloud_download_outlined),
    Card(child: Padding(padding: const EdgeInsets.all(16), child: Column(children: [
      Wrap(spacing: 12, runSpacing: 12, crossAxisAlignment: WrapCrossAlignment.center, children: [
        SizedBox(width: 300, child: TextField(controller: query,
          onSubmitted: (_) => search(),
          decoration: const InputDecoration(labelText: 'Search Hugging Face', prefixIcon: Icon(Icons.search), border: OutlineInputBorder()))),
        SizedBox(width: 240, child: TextField(controller: modelsDir,
          decoration: const InputDecoration(labelText: 'Models directory', border: OutlineInputBorder()))),
        FilledButton.icon(onPressed: busy ? null : search, icon: const Icon(Icons.travel_explore), label: const Text('Search')),
      ]),
      CheckboxListTile(value: localOnly, dense: true, contentPadding: EdgeInsets.zero,
        title: const Text('Show only models estimated to fit this machine'),
        subtitle: Text(widget.adminToken.isEmpty ? 'Without a Host credential, fit is estimated for this computer only.' : 'With an authenticated Host, ready RPC Guests contribute to memory estimates.'),
        onChanged: busy ? null : (v) => setState(() => localOnly = v ?? false)),
      Row(children: [Expanded(child: TextField(controller: manualPath,
        decoration: const InputDecoration(labelText: 'Manually downloaded GGUF path', prefixIcon: Icon(Icons.folder_open), border: OutlineInputBorder()))),
        const SizedBox(width: 8),
        OutlinedButton.icon(onPressed: busy ? null : () => runModelCommand([
          'import','--file',manualPath.text.trim(),'--dir',modelsDir.text.trim(),
        ]), icon: const Icon(Icons.file_upload_outlined), label: const Text('Import')),
      ]),
    ]))),
    if (busy) const LinearProgressIndicator(),
    Padding(padding: const EdgeInsets.symmetric(vertical: 8), child: Text(info, style: const TextStyle(color: Colors.white70))),
    if (progress.isNotEmpty) Padding(padding: const EdgeInsets.only(bottom: 8), child: Text(progress, style: const TextStyle(color: Colors.white60))),
    Expanded(child: matches.isEmpty ? Center(child: Text(busy ? 'Searching…' : 'Search for a quantized GGUF model', style: const TextStyle(color: Colors.white54))) :
      ListView.builder(itemCount: matches.length, itemBuilder: (context,index) {
        final m = matches[index];
        final bytes = (m['size_bytes'] as num?)?.toDouble() ?? 0;
        final placement = (m['placement'] ?? 'unknown').toString();
        final fit = placement == 'single-node-candidate';
        return Card(child: Padding(padding: const EdgeInsets.all(16), child: Row(children: [
          Icon(fit ? Icons.check_circle_outline : Icons.memory, color: fit ? Colors.tealAccent : Colors.amber),
          const SizedBox(width: 14),
          Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text('${m['filename']}', maxLines: 2, overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontWeight: FontWeight.w600)),
            Text('${m['repository']}  ·  ${(bytes / (1024 * 1024 * 1024)).toStringAsFixed(2)} GiB',
              style: const TextStyle(color: Colors.white60)),
            Text('${m['reason'] ?? placement}', maxLines: 2, overflow: TextOverflow.ellipsis,
              style: const TextStyle(color: Colors.white54, fontSize: 12)),
          ])),
          const SizedBox(width: 10),
          OutlinedButton.icon(onPressed: busy ? null : () => runModelCommand([
            'download','--repo','${m['repository']}','--file','${m['filename']}','--dir',modelsDir.text.trim(),
          ]), icon: const Icon(Icons.download_outlined), label: const Text('Download')),
        ])));
      })),
  ]);
}
