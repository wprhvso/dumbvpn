package ru.murasya.vpn

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.util.Log
import android.app.Notification
import android.content.pm.ServiceInfo

class MyVpnService : VpnService() {

    private var vpnInterface: ParcelFileDescriptor? = null

    companion object {
        init {
            System.loadLibrary("dumbvpn")
        }
    }

    private external fun startGoCore(fd: Int)

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        Log.d("DumbVPN", "Сервис запущен")
        
        createNotificationChannel()
        val notification = createNotification()

        startForeground(
            1, 
            notification, 
            ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED
        )

        setupTunnel()

        return START_STICKY
    }

    private fun setupTunnel() {
        try {
            val builder = Builder()
            
            builder.setSession("DumbVPN")
               .setMtu(1500)
               .addAddress("10.0.0.2", 24)
               .addRoute("0.0.0.0", 0)
               .addDnsServer("10.0.0.1")
               .addDisallowedApplication(packageName) 

            val pfd = builder.establish()
            if (pfd != null) {
                vpnInterface = pfd
                
                val fd = pfd.detachFd()
                Log.d("DumbVPN", "TUN интерфейс создан. FD = $fd")

                startGoCore(fd)
            }
        } catch (e: Exception) {
            Log.e("DumbVPN", "Ошибка создания TUN интерфейса", e)
        }
    }

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            "vpn_channel",
            "Dumb VPN Service",
            NotificationManager.IMPORTANCE_LOW
        )
        val manager = getSystemService(NotificationManager::class.java)
        manager?.createNotificationChannel(channel)
    }

    private fun createNotification(): Notification {
        val pendingIntent = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE
        )
        return Notification.Builder(this, "vpn_channel")
            .setContentTitle("Dumb VPN Активен")
            .setContentText("Трафик защищен")
            .setSmallIcon(android.R.drawable.ic_menu_share)
            .setContentIntent(pendingIntent)
            .build()
    }

    override fun onDestroy() {
        super.onDestroy()
        Log.d("DumbVPN", "Сервис остановлен")
        try {
            vpnInterface?.close()
        } catch (e: Exception) {
        }
    }
}
