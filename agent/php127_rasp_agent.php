<?php
/**
 * php127 RASP Drop-In Agent for PHP-FPM / Apache
 * SPDX-License-Identifier: Apache-2.0 OR MIT
 *
 * Injected via `auto_prepend_file = /path/to/php127_rasp_agent.php`
 * Provides in-process runtime protection, taint monitoring and telemetry push.
 */

declare(strict_types=1);

namespace Php127\Rasp;

final class Agent
{
    private static ?Agent $instance = null;
    private string $socketPath = '/tmp/php127-rasp.sock';
    private string $mode = 'audit'; // 'audit' | 'enforce'
    private array $taintedInputKeys = [];

    public static function bootstrap(string $mode = 'audit', string $socketPath = '/tmp/php127-rasp.sock'): void
    {
        if (self::$instance !== null) {
            return;
        }
        self::$instance = new self($mode, $socketPath);
        self::$instance->init();
    }

    private function __construct(string $mode, string $socketPath)
    {
        $this->mode = getenv('PHP127_RASP_MODE') ?: $mode;
        $this->socketPath = getenv('PHP127_RASP_SOCK') ?: $socketPath;
    }

    private function init(): void
    {
        // 1. Taint inspection on superglobals
        $this->inspectSuperglobals();

        // 2. Register shutdown and error telemetry
        register_shutdown_function([$this, 'onShutdown']);
        set_error_handler([$this, 'onError']);
    }

    private function inspectSuperglobals(): void
    {
        $sources = [
            'GET'    => $_GET,
            'POST'   => $_POST,
            'COOKIE' => $_COOKIE,
        ];

        foreach ($sources as $sourceName => $data) {
            if (!is_array($data)) {
                continue;
            }
            foreach ($data as $k => $v) {
                if (is_string($v)) {
                    $this->analyzePayload($sourceName, (string)$k, $v);
                }
            }
        }
    }

    private function analyzePayload(string $source, string $key, string $value): void
    {
        // Check for serialized objects injection
        if (preg_match('/^[OaC]:\d+:"[^"]+":\d+:/', $value)) {
            $this->notifyViolation('UNSERIALIZE_ATTACK', "$source:$key", $value);
        }

        // Check for null-byte injections
        if (str_contains($value, "\0")) {
            $this->notifyViolation('NULL_BYTE_INJECTION', "$source:$key", $value);
        }

        // Check for PHP wrapper stream attacks (php://input, data://, etc.)
        if (preg_match('#^(php|data|zip|phar|glob)://#i', $value)) {
            $this->notifyViolation('STREAM_WRAPPER_EXPLOIT', "$source:$key", $value);
        }
    }

    public function notifyViolation(string $type, string $location, string $payload): void
    {
        $event = [
            'timestamp' => microtime(true),
            'type'      => $type,
            'location'  => $location,
            'sha256'    => hash('sha256', $payload),
            'uri'       => $_SERVER['REQUEST_URI'] ?? 'CLI',
            'client_ip' => $_SERVER['REMOTE_ADDR'] ?? '127.0.0.1',
            'mode'      => $this->mode,
        ];

        $this->sendToDaemon($event);

        if ($this->mode === 'enforce') {
            http_response_code(403);
            header('Content-Type: application/json');
            echo json_encode([
                'error'     => 'Forbidden by php127 In-Process RASP Agent',
                'violation' => $type,
                'ref'       => substr($event['sha256'], 0, 16),
            ]);
            exit(1);
        }
    }

    private function sendToDaemon(array $event): void
    {
        if (!file_exists($this->socketPath)) {
            return;
        }

        $socket = @socket_create(AF_UNIX, SOCK_DGRAM, 0);
        if ($socket) {
            $json = json_encode($event) . "\n";
            @socket_sendto($socket, $json, strlen($json), 0, $this->socketPath, 0);
            @socket_close($socket);
        }
    }

    public function onError(int $errno, string $errstr, string $errfile, int $errline): bool
    {
        if (str_contains($errstr, 'unserialize(): Error at offset') || str_contains($errstr, 'include(): Failed opening')) {
            $this->notifyViolation('PHP_RUNTIME_SINK_FAULT', "$errfile:$errline", $errstr);
        }
        return false;
    }

    public function onShutdown(): void
    {
        $err = error_get_last();
        if ($err !== null && ($err['type'] & (E_ERROR | E_PARSE | E_CORE_ERROR | E_COMPILE_ERROR))) {
            if (str_contains($err['message'], 'eval()') || str_contains($err['message'], 'call_user_func')) {
                $this->notifyViolation('FATAL_SINK_CRASH', "{$err['file']}:{$err['line']}", $err['message']);
            }
        }
    }
}

// Auto-boot if not in CLI or if explicitly enabled
if (php_sapi_name() !== 'cli' || getenv('PHP127_RASP_FORCE') === '1') {
    Agent::bootstrap();
}
